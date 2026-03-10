package organizer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultGroupSize = 30
	minGroupSize     = 1
	maxGroupSize     = 1000
)

type Options struct {
	GroupSize int
	Now       func() time.Time
	Progress  func(done, total int, message string)
}

type Result struct {
	Processed int
	Groups    int
	DateStamp string
}

type sourceFile struct {
	name    string
	path    string
	modTime time.Time
	ext     string
}

type plannedFile struct {
	number       string
	originalPath string
	currentPath  string
	tempPath     string
	finalName    string
	finalPath    string
}

type plannedGroup struct {
	name  string
	path  string
	files []*plannedFile
}

type plan struct {
	files  []plannedFile
	groups []plannedGroup
}

type tracker struct {
	total    int
	done     int
	progress func(done, total int, message string)
}

func ProcessDirectory(root string, opts Options) (Result, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	groupSize := opts.GroupSize
	if groupSize == 0 {
		groupSize = defaultGroupSize
	}
	if groupSize < minGroupSize || groupSize > maxGroupSize {
		return Result{}, fmt.Errorf("group size must be between %d and %d", minGroupSize, maxGroupSize)
	}

	t := tracker{
		progress: opts.Progress,
	}
	t.report("正在扫描 MP4 文件")

	files, err := scanMP4Files(root)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		return Result{}, errors.New("程序所在目录没有可处理的 mp4 文件")
	}
	if len(files) > 9999 {
		return Result{}, fmt.Errorf("mp4 文件数量为 %d，超过四位编号上限 9999", len(files))
	}

	dateStamp := now().Format("20060102")
	workPlan, err := buildPlan(root, files, dateStamp, groupSize)
	if err != nil {
		return Result{}, err
	}
	if err := validatePlan(workPlan); err != nil {
		return Result{}, err
	}

	t.total = len(workPlan.files)*3 + len(workPlan.groups)
	t.report(fmt.Sprintf("共找到 %d 个视频，准备处理", len(workPlan.files)))

	createdDirs := make([]string, 0, len(workPlan.groups))
	if err := executePlan(workPlan, &t, &createdDirs); err != nil {
		rollbackErr := rollbackPlan(workPlan, createdDirs)
		if rollbackErr != nil {
			return Result{}, fmt.Errorf("%w；回滚失败：%v", err, rollbackErr)
		}
		return Result{}, err
	}

	return Result{
		Processed: len(workPlan.files),
		Groups:    len(workPlan.groups),
		DateStamp: dateStamp,
	}, nil
}

func scanMP4Files(root string) ([]sourceFile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败：%w", err)
	}

	files := make([]sourceFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := filepath.Ext(name)
		if !strings.EqualFold(ext, ".mp4") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("读取文件信息失败 %q：%w", name, err)
		}

		files = append(files, sourceFile{
			name:    name,
			path:    filepath.Join(root, name),
			modTime: info.ModTime(),
			ext:     ext,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return strings.ToLower(files[i].name) < strings.ToLower(files[j].name)
		}
		return files[i].modTime.Before(files[j].modTime)
	})

	return files, nil
}

func buildPlan(root string, files []sourceFile, dateStamp string, groupSize int) (plan, error) {
	stamp := time.Now().UnixNano()
	workPlan := plan{
		files: make([]plannedFile, len(files)),
	}

	for i, file := range files {
		number := fmt.Sprintf("%04d", i+1)
		finalName := number + "_" + dateStamp + file.ext
		tempName := fmt.Sprintf(".__vido_tmp__%d__%04d%s", stamp, i+1, file.ext)

		workPlan.files[i] = plannedFile{
			number:       number,
			originalPath: file.path,
			currentPath:  file.path,
			tempPath:     filepath.Join(root, tempName),
			finalName:    finalName,
			finalPath:    filepath.Join(root, finalName),
		}
	}

	groupCount := (len(workPlan.files) + groupSize - 1) / groupSize
	workPlan.groups = make([]plannedGroup, 0, groupCount)
	for start := 0; start < len(workPlan.files); start += groupSize {
		end := start + groupSize
		if end > len(workPlan.files) {
			end = len(workPlan.files)
		}

		groupName := fmt.Sprintf(
			"%s-%s_%s",
			workPlan.files[start].number,
			workPlan.files[end-1].number,
			dateStamp,
		)
		group := plannedGroup{
			name:  groupName,
			path:  filepath.Join(root, groupName),
			files: make([]*plannedFile, 0, end-start),
		}
		for i := start; i < end; i++ {
			group.files = append(group.files, &workPlan.files[i])
		}
		workPlan.groups = append(workPlan.groups, group)
	}

	return workPlan, nil
}

func validatePlan(workPlan plan) error {
	originals := make(map[string]struct{}, len(workPlan.files))
	for _, file := range workPlan.files {
		originals[file.originalPath] = struct{}{}
	}

	for _, group := range workPlan.groups {
		info, err := os.Stat(group.path)
		if err == nil {
			if info.IsDir() {
				return fmt.Errorf("目标子文件夹已存在：%s", group.name)
			}
			return fmt.Errorf("目标路径已存在且不是文件夹：%s", group.name)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("检查目标子文件夹失败 %q：%w", group.name, err)
		}
	}

	for _, file := range workPlan.files {
		if _, exists := originals[file.tempPath]; exists {
			return fmt.Errorf("临时文件名冲突：%s", filepath.Base(file.tempPath))
		}

		if _, err := os.Stat(file.tempPath); err == nil {
			return fmt.Errorf("临时文件已存在：%s", filepath.Base(file.tempPath))
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查临时文件失败 %q：%w", filepath.Base(file.tempPath), err)
		}

		if _, err := os.Stat(file.finalPath); err == nil {
			if _, ok := originals[file.finalPath]; !ok {
				return fmt.Errorf("目标文件已存在：%s", filepath.Base(file.finalPath))
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查目标文件失败 %q：%w", filepath.Base(file.finalPath), err)
		}
	}

	return nil
}

func executePlan(workPlan plan, t *tracker, createdDirs *[]string) error {
	for i := range workPlan.files {
		file := &workPlan.files[i]
		if err := os.Rename(file.currentPath, file.tempPath); err != nil {
			return fmt.Errorf("暂存重命名失败 %q：%w", filepath.Base(file.currentPath), err)
		}
		file.currentPath = file.tempPath
		t.advance(fmt.Sprintf("正在暂存重命名 %d/%d", i+1, len(workPlan.files)))
	}

	for i := range workPlan.files {
		file := &workPlan.files[i]
		if err := os.Rename(file.currentPath, file.finalPath); err != nil {
			return fmt.Errorf("正式重命名失败 %q：%w", filepath.Base(file.originalPath), err)
		}
		file.currentPath = file.finalPath
		t.advance(fmt.Sprintf("正在生成目标文件名 %d/%d", i+1, len(workPlan.files)))
	}

	for i := range workPlan.groups {
		group := &workPlan.groups[i]
		if err := os.Mkdir(group.path, 0o755); err != nil {
			return fmt.Errorf("创建子文件夹失败 %q：%w", group.name, err)
		}
		*createdDirs = append(*createdDirs, group.path)
		t.advance(fmt.Sprintf("已创建子文件夹 %s", group.name))

		for index, file := range group.files {
			targetPath := filepath.Join(group.path, filepath.Base(file.finalPath))
			if err := os.Rename(file.currentPath, targetPath); err != nil {
				return fmt.Errorf("移动文件失败 %q：%w", filepath.Base(file.currentPath), err)
			}
			file.currentPath = targetPath
			t.advance(fmt.Sprintf("正在归档分组 %s (%d/%d)", group.name, index+1, len(group.files)))
		}
	}

	return nil
}

func rollbackPlan(workPlan plan, createdDirs []string) error {
	var errs []string

	for i := len(workPlan.files) - 1; i >= 0; i-- {
		file := &workPlan.files[i]
		if file.currentPath == file.originalPath {
			continue
		}
		if _, err := os.Stat(file.currentPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, fmt.Sprintf("检查回滚文件失败 %s：%v", filepath.Base(file.currentPath), err))
			continue
		}

		if err := os.Rename(file.currentPath, file.originalPath); err != nil {
			errs = append(errs, fmt.Sprintf("回滚文件失败 %s：%v", filepath.Base(file.originalPath), err))
			continue
		}
		file.currentPath = file.originalPath
	}

	for i := len(createdDirs) - 1; i >= 0; i-- {
		if err := os.Remove(createdDirs[i]); err != nil && !os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("删除回滚目录失败 %s：%v", filepath.Base(createdDirs[i]), err))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "；"))
	}
	return nil
}

func (t *tracker) report(message string) {
	if t.progress != nil {
		t.progress(t.done, t.total, message)
	}
}

func (t *tracker) advance(message string) {
	t.done++
	t.report(message)
}
