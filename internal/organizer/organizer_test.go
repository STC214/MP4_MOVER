package organizer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessDirectoryRenamesAndGroupsFiles(t *testing.T) {
	root := t.TempDir()
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	for i := 0; i < 31; i++ {
		name := filepath.Join(root, "video_"+time.Duration(i).String()+".mp4")
		if err := os.WriteFile(name, []byte("test"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		modTime := baseTime.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(name, modTime, modTime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	result, err := ProcessDirectory(root, Options{
		GroupSize: 30,
		Now: func() time.Time {
			return baseTime
		},
	})
	if err != nil {
		t.Fatalf("ProcessDirectory returned error: %v", err)
	}

	if result.Processed != 31 {
		t.Fatalf("processed = %d, want 31", result.Processed)
	}
	if result.Groups != 2 {
		t.Fatalf("groups = %d, want 2", result.Groups)
	}

	firstGroup := filepath.Join(root, "0001-0030_20260310")
	secondGroup := filepath.Join(root, "0031-0031_20260310")

	assertFileExists(t, filepath.Join(firstGroup, "0001_20260310.mp4"))
	assertFileExists(t, filepath.Join(firstGroup, "0030_20260310.mp4"))
	assertFileExists(t, filepath.Join(secondGroup, "0031_20260310.mp4"))

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir root: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("root entries = %d, want 2", len(entries))
	}
}

func TestProcessDirectoryRejectsExistingGroupFolder(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "sample.mp4")
	if err := os.WriteFile(videoPath, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	groupDir := filepath.Join(root, "0001-0001_20260310")
	if err := os.Mkdir(groupDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := ProcessDirectory(root, Options{
		GroupSize: 30,
		Now: func() time.Time {
			return time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
		},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestProcessDirectoryUsesCustomGroupSize(t *testing.T) {
	root := t.TempDir()
	baseTime := time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

	for i := 0; i < 10; i++ {
		name := filepath.Join(root, "clip_"+time.Duration(i).String()+".mp4")
		if err := os.WriteFile(name, []byte("test"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		modTime := baseTime.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(name, modTime, modTime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	result, err := ProcessDirectory(root, Options{
		GroupSize: 4,
		Now: func() time.Time {
			return baseTime
		},
	})
	if err != nil {
		t.Fatalf("ProcessDirectory returned error: %v", err)
	}

	if result.Groups != 3 {
		t.Fatalf("groups = %d, want 3", result.Groups)
	}

	assertFileExists(t, filepath.Join(root, "0001-0004_20260310", "0001_20260310.mp4"))
	assertFileExists(t, filepath.Join(root, "0005-0008_20260310", "0008_20260310.mp4"))
	assertFileExists(t, filepath.Join(root, "0009-0010_20260310", "0010_20260310.mp4"))
}

func TestProcessDirectoryRejectsInvalidGroupSize(t *testing.T) {
	root := t.TempDir()
	videoPath := filepath.Join(root, "sample.mp4")
	if err := os.WriteFile(videoPath, []byte("test"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := ProcessDirectory(root, Options{
		GroupSize: 1001,
		Now: func() time.Time {
			return time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)
		},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("%s is a directory, want file", path)
	}
}
