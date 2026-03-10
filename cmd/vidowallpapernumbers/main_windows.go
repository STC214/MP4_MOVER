//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"vido_wallpaper_numbers/internal/diaglog"
	"vido_wallpaper_numbers/internal/organizer"
)

const (
	appClassName  = "VidoWallpaperNumbersWindow"
	appTitle      = "Vido Wallpaper Numbers"
	appIconID     = 101
	appConfigFile = "VidoWallpaperNumbers.config.json"

	editGroupSizeID = 1002

	defaultWindowWidth  = 300
	defaultWindowHeight = 150
	defaultGroupSize    = 30

	wmCreate        = 0x0001
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmPaint         = 0x000F
	wmGetText       = 0x000D
	wmSetFocus      = 0x0007
	wmSize          = 0x0005
	wmSetFont       = 0x0030
	wmCtlColorEdit  = 0x0133
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmLButtonDblClk = 0x0203
	wmApp           = 0x8000
	wmWorkerEvent   = wmApp + 1
	wmHealthPing    = wmApp + 2

	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsVisible      = 0x10000000
	wsChild        = 0x40000000
	wsBorder       = 0x00800000
	esCenter       = 0x0001
	esAutoHScroll  = 0x0080
	esNumber       = 0x2000
	cwUseDefault   = ^uintptr(0x7fffffff)
	swShow         = 5
	defaultGUIFont = 17
	mbIconError    = 0x00000010
	mbIconWarning  = 0x00000030
	mbOK           = 0x00000000
	gwlpWndProc    = -4
	emSetLimitText = 0x00C5
	emSetSel       = 0x00B1

	imageIcon       = 1
	lrDefaultColor  = 0x0000
	transparentMode = 1

	drawTextCenter     = 0x00000001
	drawTextVCenter    = 0x00000004
	drawTextSingleLine = 0x00000020

	dwmwaUseImmersiveDarkMode = 20
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36

	maxEventsPerDispatch = 32
)

type (
	hwnd      uintptr
	hinstance uintptr
	hmenu     uintptr
)

type point struct {
	X int32
	Y int32
}

type msg struct {
	HWnd     hwnd
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type rect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type paintStruct struct {
	HDC         uintptr
	FErase      int32
	RcPaint     rect
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     hinstance
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type workerEvent struct {
	done     int
	total    int
	message  string
	finished bool
	err      error
}

type appConfig struct {
	WindowX   int32 `json:"window_x"`
	WindowY   int32 `json:"window_y"`
	Width     int32 `json:"width"`
	Height    int32 `json:"height"`
	GroupSize int   `json:"group_size"`
}

type appWindow struct {
	hwnd           hwnd
	groupInput     hwnd
	groupInputProc uintptr
	font           uintptr
	inputFont      uintptr
	events         chan workerEvent
	running        bool
	exeDir         string
	configPath     string
	config         appConfig
	progressPct    int
	buttonPressed  bool
	buttonRect     rect
	progressRect   rect
	eventPosted    uint32
	dragLogged     uint32
	lastHealthPing int64
	lastHealthPong int64
	watchdogStop   chan struct{}
}

type theme struct {
	windowColor      uint32
	surfaceColor     uint32
	textColor        uint32
	borderColor      uint32
	buttonColor      uint32
	buttonHoverColor uint32
	buttonDisabled   uint32
	progressTrack    uint32
	progressFill     uint32
	windowBrush      uintptr
	surfaceBrush     uintptr
	borderBrush      uintptr
	buttonBrush      uintptr
	buttonHoverBrush uintptr
	buttonDisabledBr uintptr
	progressTrackBr  uintptr
	progressFillBr   uintptr
}

var (
	currentApp = &appWindow{
		events:       make(chan workerEvent, 256),
		watchdogStop: make(chan struct{}),
	}
	editWndProcCallback = syscall.NewCallback(groupInputProc)

	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	procBeginPaint          = user32.NewProc("BeginPaint")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procCallWindowProc      = user32.NewProc("CallWindowProcW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procDrawText            = user32.NewProc("DrawTextW")
	procEnableWindow        = user32.NewProc("EnableWindow")
	procEndPaint            = user32.NewProc("EndPaint")
	procFillRect            = user32.NewProc("FillRect")
	procFrameRect           = user32.NewProc("FrameRect")
	procGetClientRect       = user32.NewProc("GetClientRect")
	procGetMessage          = user32.NewProc("GetMessageW")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procInvalidateRect      = user32.NewProc("InvalidateRect")
	procLoadCursor          = user32.NewProc("LoadCursorW")
	procLoadImage           = user32.NewProc("LoadImageW")
	procMessageBox          = user32.NewProc("MessageBoxW")
	procMoveWindow          = user32.NewProc("MoveWindow")
	procPostMessage         = user32.NewProc("PostMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procSendMessage         = user32.NewProc("SendMessageW")
	procSetFocus            = user32.NewProc("SetFocus")
	procSetWindowLongPtr    = user32.NewProc("SetWindowLongPtrW")
	procSetWindowText       = user32.NewProc("SetWindowTextW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procUpdateWindow        = user32.NewProc("UpdateWindow")

	procGetModuleHandle    = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")

	procCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	procCreateFont       = gdi32.NewProc("CreateFontW")
	procGetStockObject   = gdi32.NewProc("GetStockObject")
	procSetBkColor       = gdi32.NewProc("SetBkColor")
	procSetBkMode        = gdi32.NewProc("SetBkMode")
	procSetTextColor     = gdi32.NewProc("SetTextColor")

	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")

	appTheme theme
)

func main() {
	defer handleMainPanic()

	exePath, err := os.Executable()
	if err != nil {
		showMessage(0, "无法确定程序所在目录："+err.Error(), mbIconError|mbOK)
		return
	}

	currentApp.exeDir = filepath.Dir(exePath)
	currentApp.configPath = filepath.Join(currentApp.exeDir, appConfigFile)
	if err := run(); err != nil {
		diaglog.Logf("run failed: %v", err)
		showMessage(0, err.Error(), mbIconError|mbOK)
	}
}

func run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	diaglog.Logf("run start")
	diaglog.Logf("ui thread locked tid=%d", currentThreadID())
	initTheme()
	currentApp.loadConfig()
	diaglog.Logf("config loaded window=(%d,%d %dx%d) group_size=%d", currentApp.config.WindowX, currentApp.config.WindowY, currentApp.config.Width, currentApp.config.Height, currentApp.config.GroupSize)

	instance, err := getModuleHandle()
	if err != nil {
		return err
	}

	className := utf16Ptr(appClassName)
	cursor, _, _ := procLoadCursor.Call(0, uintptr(32512))
	largeIcon, err := loadAppIcon(instance, 32, 32)
	if err != nil {
		return err
	}
	smallIcon, err := loadAppIcon(instance, 16, 16)
	if err != nil {
		return err
	}

	class := wndClassEx{
		CbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		LpfnWndProc:   syscall.NewCallback(windowProc),
		HInstance:     instance,
		HIcon:         largeIcon,
		HCursor:       cursor,
		HbrBackground: appTheme.windowBrush,
		LpszClassName: className,
		HIconSm:       smallIcon,
	}

	atom, _, registerErr := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return fmt.Errorf("注册窗口类失败：%v", registerErr)
	}
	diaglog.Logf("window class registered")

	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsMinimizeBox | wsVisible)
	title := utf16Ptr(appTitle)
	x, y, width, height := currentApp.windowBounds()
	handle, _, createErr := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		style,
		x,
		y,
		width,
		height,
		0,
		0,
		uintptr(instance),
		0,
	)
	if handle == 0 {
		return fmt.Errorf("创建窗口失败：%v", createErr)
	}

	currentApp.hwnd = hwnd(handle)
	diaglog.Logf("window created hwnd=%#x tid=%d", handle, currentThreadID())
	applyDarkTitleBar(currentApp.hwnd)
	currentApp.layout()
	invalidate(currentApp.hwnd)
	procShowWindow.Call(handle, swShow)
	procUpdateWindow.Call(handle)

	var message msg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			return fmt.Errorf("消息循环失败")
		case 0:
			diaglog.Logf("message loop exit tid=%d", currentThreadID())
			return nil
		default:
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
			procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
		}
	}
}

func handleMainPanic() {
	if recovered := recover(); recovered != nil {
		diaglog.Logf("panic in main: %v\n%s", recovered, debug.Stack())
		showMessage(0, fmt.Sprintf("程序发生未处理异常：%v", recovered), mbIconError|mbOK)
	}
}

func (a *appWindow) handleWorkerPanic() {
	if recovered := recover(); recovered != nil {
		diaglog.Logf("panic in worker: %v\n%s", recovered, debug.Stack())
		a.pushEvent(workerEvent{
			finished: true,
			err:      fmt.Errorf("程序内部异常：%v", recovered),
		})
	}
}

func (a *appWindow) startWatchdog() {
	atomic.StoreInt64(&a.lastHealthPing, time.Now().UnixNano())
	atomic.StoreInt64(&a.lastHealthPong, time.Now().UnixNano())

	go func(window hwnd) {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				pingAt := time.Now()
				atomic.StoreInt64(&a.lastHealthPing, pingAt.UnixNano())
				ret, _, _ := procPostMessage.Call(uintptr(window), wmHealthPing, 0, 0)
				if ret == 0 {
					diaglog.Logf("watchdog failed to post health ping hwnd=%#x", window)
					continue
				}

				lastPong := atomic.LoadInt64(&a.lastHealthPong)
				if pingAt.UnixNano()-lastPong > int64(6*time.Second) {
					diaglog.Logf("watchdog detected ui heartbeat delay delay_ms=%d running=%t progress=%d queue_len=%d", (pingAt.UnixNano()-lastPong)/int64(time.Millisecond), a.running, a.progressPct, len(a.events))
				}
			case <-a.watchdogStop:
				diaglog.Logf("watchdog stopped")
				return
			}
		}
	}(a.hwnd)
}

func (a *appWindow) stopWatchdog() {
	select {
	case <-a.watchdogStop:
	default:
		close(a.watchdogStop)
	}
}

func windowProc(window hwnd, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCreate:
		diaglog.Logf("window message create")
		currentApp.hwnd = window
		if err := currentApp.createControls(window); err != nil {
			showMessage(window, err.Error(), mbIconError|mbOK)
			procDestroyWindow.Call(uintptr(window))
			return 0
		}
		currentApp.layout()
		currentApp.setStatus("每组 1-1000，默认 30")
		return 0
	case wmSize:
		diaglog.Logf("window message size")
		currentApp.hwnd = window
		currentApp.layout()
		invalidate(window)
		return 0
	case wmCtlColorEdit:
		hdc := wParam
		procSetTextColor.Call(hdc, uintptr(appTheme.textColor))
		procSetBkColor.Call(hdc, uintptr(appTheme.surfaceColor))
		return appTheme.surfaceBrush
	case wmLButtonDown:
		x, y := coordsFromLParam(lParam)
		if pointInRect(x, y, currentApp.buttonRect) && !currentApp.running {
			currentApp.buttonPressed = true
			invalidate(window)
			return 0
		}
	case wmLButtonUp:
		x, y := coordsFromLParam(lParam)
		pressed := currentApp.buttonPressed
		currentApp.buttonPressed = false
		invalidate(window)
		if pressed && pointInRect(x, y, currentApp.buttonRect) && !currentApp.running {
			currentApp.startWork()
			return 0
		}
	case wmPaint:
		currentApp.paint(window)
		return 0
	case wmWorkerEvent:
		currentApp.handleWorkerMessage()
		return 0
	case wmHealthPing:
		atomic.StoreInt64(&currentApp.lastHealthPong, time.Now().UnixNano())
		diaglog.Logf("window message health ping acknowledged tid=%d", currentThreadID())
		return 0
	case wmClose:
		diaglog.Logf("window message close running=%t", currentApp.running)
		if currentApp.running {
			showMessage(window, "任务正在执行，请等待完成后再关闭。", mbIconWarning|mbOK)
			return 0
		}
		if err := currentApp.saveCurrentConfig(); err != nil {
			showMessage(window, "保存配置失败："+err.Error(), mbIconWarning|mbOK)
		}
		procDestroyWindow.Call(uintptr(window))
		return 0
	case wmDestroy:
		currentApp.stopWatchdog()
		diaglog.Logf("window message destroy")
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProc.Call(uintptr(window), uintptr(message), wParam, lParam)
	return ret
}

func (a *appWindow) createControls(window hwnd) error {
	diaglog.Logf("create controls start")
	font, _, _ := procGetStockObject.Call(defaultGUIFont)
	a.font = font
	a.inputFont = createFont(-22, 600, "Segoe UI")
	if a.inputFont == 0 {
		a.inputFont = font
	}

	groupSizeText := strconv.Itoa(defaultGroupSize)
	if a.config.GroupSize >= 1 && a.config.GroupSize <= 1000 {
		groupSizeText = strconv.Itoa(a.config.GroupSize)
	}

	groupInput, err := createChildControl(
		window,
		"EDIT",
		groupSizeText,
		wsChild|wsVisible|wsBorder|esCenter|esAutoHScroll|esNumber,
		0,
		hmenu(editGroupSizeID),
	)
	if err != nil {
		return err
	}

	a.groupInput = groupInput
	sendMessage(groupInput, wmSetFont, a.inputFont, 1)
	sendMessage(groupInput, emSetLimitText, 4, 0)
	if err := a.installGroupInputProc(); err != nil {
		return err
	}
	diaglog.Logf("create controls done group_input=%#x", groupInput)
	return nil
}

func (a *appWindow) installGroupInputProc() error {
	if a.groupInput == 0 {
		return nil
	}

	wndProcIndex := int32(gwlpWndProc)
	oldProc, _, err := procSetWindowLongPtr.Call(
		uintptr(a.groupInput),
		uintptr(wndProcIndex),
		editWndProcCallback,
	)
	if oldProc == 0 {
		return fmt.Errorf("安装数字输入框消息处理失败：%v", err)
	}

	a.groupInputProc = oldProc
	diaglog.Logf("group input subclass installed hwnd=%#x old_proc=%#x", a.groupInput, oldProc)
	return nil
}

func (a *appWindow) layout() {
	if a.hwnd == 0 {
		return
	}

	var rc rect
	procGetClientRect.Call(uintptr(a.hwnd), uintptr(unsafe.Pointer(&rc)))

	width := int(rc.Right - rc.Left)
	height := int(rc.Bottom - rc.Top)
	if width <= 0 {
		return
	}

	inputWidth := 60
	buttonWidth := 132
	gap := 12
	rowHeight := 36
	progressHeight := 22
	rowWidth := inputWidth + gap + buttonWidth
	groupHeight := rowHeight + 14 + progressHeight

	left := (width - rowWidth) / 2
	if left < 12 {
		left = 12
	}
	top := (height - groupHeight) / 2
	if top < 12 {
		top = 12
	}
	progressTop := top + rowHeight + 14

	moveWindow(a.groupInput, left, top, inputWidth, rowHeight)

	a.buttonRect = rect{
		Left:   int32(left + inputWidth + gap),
		Top:    int32(top),
		Right:  int32(left + inputWidth + gap + buttonWidth),
		Bottom: int32(top + rowHeight),
	}
	a.progressRect = rect{
		Left:   int32(left),
		Top:    int32(progressTop),
		Right:  int32(left + rowWidth),
		Bottom: int32(progressTop + progressHeight),
	}
	diaglog.Logf("layout updated input=(%d,%d,%d,%d) button=(%d,%d,%d,%d) progress=(%d,%d,%d,%d)", left, top, inputWidth, rowHeight, a.buttonRect.Left, a.buttonRect.Top, a.buttonRect.Right, a.buttonRect.Bottom, a.progressRect.Left, a.progressRect.Top, a.progressRect.Right, a.progressRect.Bottom)
}

func (a *appWindow) paint(window hwnd) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(window), uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(uintptr(window), uintptr(unsafe.Pointer(&ps)))

	drawButton(hdc, a.buttonRect, a.running, a.buttonPressed)
	drawProgress(hdc, a.progressRect, a.progressPct)
}

func (a *appWindow) startWork() {
	if a.running {
		return
	}

	groupSize, err := a.groupSize()
	if err != nil {
		diaglog.Logf("start work blocked by invalid group size: %v", err)
		showMessage(a.hwnd, err.Error(), mbIconWarning|mbOK)
		return
	}

	a.running = true
	a.resetEvents()
	enableWindow(a.groupInput, false)
	a.setProgress(0)
	a.setStatus("正在处理")
	diaglog.Logf("start work group_size=%d exe_dir=%q", groupSize, a.exeDir)

	go func(exeDir string, groupSize int) {
		defer a.handleWorkerPanic()
		result, err := organizer.ProcessDirectory(exeDir, organizer.Options{
			GroupSize: groupSize,
			Now:       time.Now,
			Logf:      diaglog.Logf,
			Progress: func(done, total int, message string) {
				a.pushEvent(workerEvent{
					done:    done,
					total:   total,
					message: message,
				})
			},
		})
		if err != nil {
			diaglog.Logf("worker finished with error: %v", err)
			a.pushEvent(workerEvent{
				finished: true,
				err:      err,
			})
			return
		}

		a.pushEvent(workerEvent{
			done:     100,
			total:    100,
			message:  fmt.Sprintf("处理完成：%d 个视频，%d 个子文件夹，每组 %d 个", result.Processed, result.Groups, groupSize),
			finished: true,
		})
		diaglog.Logf("worker finished successfully processed=%d groups=%d", result.Processed, result.Groups)
	}(a.exeDir, groupSize)

}

func (a *appWindow) drainEvents() {
	for i := 0; i < maxEventsPerDispatch; i++ {
		select {
		case event := <-a.events:
			a.handleEvent(event)
		default:
			return
		}
	}
}

func (a *appWindow) handleWorkerMessage() {
	atomic.StoreUint32(&a.eventPosted, 0)
	diaglog.Logf("handle worker message queue_len=%d", len(a.events))
	a.drainEvents()
	if len(a.events) > 0 {
		a.postWorkerMessage()
	}
}

func (a *appWindow) handleEvent(event workerEvent) {
	if event.total > 0 {
		percent := event.done * 100 / event.total
		a.setProgress(percent)
	}
	if event.message != "" {
		a.setStatus(event.message)
	}
	if event.finished {
		diaglog.Logf("handle finished event err=%v progress=%d", event.err, a.progressPct)
		a.running = false
		enableWindow(a.groupInput, true)
		invalidate(a.hwnd)
		if event.err != nil {
			a.setStatus("处理失败")
			showMessage(a.hwnd, event.err.Error(), mbIconError|mbOK)
			return
		}
		a.setProgress(100)
	}
}

func (a *appWindow) pushEvent(event workerEvent) {
	if !a.enqueueEvent(event) {
		diaglog.Logf("drop worker event finished=%t queue_len=%d", event.finished, len(a.events))
		return
	}
	a.postWorkerMessage()
}

func (a *appWindow) enqueueEvent(event workerEvent) bool {
	select {
	case a.events <- event:
		return true
	default:
	}

	if !event.finished {
		return false
	}

	for {
		select {
		case <-a.events:
		default:
			return false
		}

		select {
		case a.events <- event:
			return true
		default:
		}
	}
}

func (a *appWindow) postWorkerMessage() {
	if a.hwnd == 0 {
		return
	}
	if !atomic.CompareAndSwapUint32(&a.eventPosted, 0, 1) {
		return
	}
	ret, _, _ := procPostMessage.Call(uintptr(a.hwnd), wmWorkerEvent, 0, 0)
	if ret == 0 {
		diaglog.Logf("post worker message failed hwnd=%#x", a.hwnd)
		atomic.StoreUint32(&a.eventPosted, 0)
	}
}

func (a *appWindow) resetEvents() {
	for {
		select {
		case <-a.events:
		default:
			atomic.StoreUint32(&a.eventPosted, 0)
			return
		}
	}
}

func (a *appWindow) setStatus(text string) {
	if text == "" {
		setWindowText(a.hwnd, appTitle)
		return
	}
	setWindowText(a.hwnd, appTitle+" - "+text)
}

func (a *appWindow) setProgress(percent int) {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	a.progressPct = percent
	invalidate(a.hwnd)
}

func (a *appWindow) groupSize() (int, error) {
	text := getWindowText(a.groupInput)
	if text == "" {
		return 0, fmt.Errorf("每组视频数量不能为空")
	}

	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("每组视频数量必须是 1 到 1000 的整数")
	}
	if value < 1 || value > 1000 {
		return 0, fmt.Errorf("每组视频数量必须在 1 到 1000 之间")
	}

	return value, nil
}

func (a *appWindow) loadConfig() {
	a.config = appConfig{
		Width:     defaultWindowWidth,
		Height:    defaultWindowHeight,
		GroupSize: defaultGroupSize,
	}

	if a.configPath == "" {
		diaglog.Logf("load config skipped: empty path")
		return
	}

	data, err := os.ReadFile(a.configPath)
	if err != nil {
		diaglog.Logf("load config skipped path=%q err=%v", a.configPath, err)
		return
	}

	var cfg appConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		diaglog.Logf("load config invalid path=%q err=%v", a.configPath, err)
		return
	}

	if cfg.Width >= 200 && cfg.Width <= 2000 {
		a.config.Width = cfg.Width
	}
	if cfg.Height >= 120 && cfg.Height <= 1200 {
		a.config.Height = cfg.Height
	}
	if cfg.GroupSize >= 1 && cfg.GroupSize <= 1000 {
		a.config.GroupSize = cfg.GroupSize
	}
	a.config.WindowX = cfg.WindowX
	a.config.WindowY = cfg.WindowY
	diaglog.Logf("load config applied path=%q window=(%d,%d %dx%d) group_size=%d", a.configPath, a.config.WindowX, a.config.WindowY, a.config.Width, a.config.Height, a.config.GroupSize)
}

func (a *appWindow) saveCurrentConfig() error {
	if a.configPath == "" || a.hwnd == 0 {
		diaglog.Logf("save config skipped path=%q hwnd=%#x", a.configPath, a.hwnd)
		return nil
	}

	cfg := a.config
	var windowRect rect
	ret, _, err := procGetWindowRect.Call(uintptr(a.hwnd), uintptr(unsafe.Pointer(&windowRect)))
	if ret == 0 {
		return fmt.Errorf("获取窗口位置失败：%v", err)
	}

	cfg.WindowX = windowRect.Left
	cfg.WindowY = windowRect.Top
	cfg.Width = windowRect.Right - windowRect.Left
	cfg.Height = windowRect.Bottom - windowRect.Top

	if groupSize, err := a.groupSize(); err == nil {
		cfg.GroupSize = groupSize
	}
	if cfg.GroupSize < 1 || cfg.GroupSize > 1000 {
		cfg.GroupSize = defaultGroupSize
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(a.configPath, data, 0o644); err != nil {
		return err
	}

	a.config = cfg
	diaglog.Logf("save config done path=%q window=(%d,%d %dx%d) group_size=%d", a.configPath, cfg.WindowX, cfg.WindowY, cfg.Width, cfg.Height, cfg.GroupSize)
	return nil
}

func (a *appWindow) windowBounds() (uintptr, uintptr, uintptr, uintptr) {
	width := a.config.Width
	height := a.config.Height
	if width <= 0 {
		width = defaultWindowWidth
	}
	if height <= 0 {
		height = defaultWindowHeight
	}

	x := uintptr(cwUseDefault)
	y := uintptr(cwUseDefault)
	if a.config.WindowX != 0 || a.config.WindowY != 0 {
		x = uintptr(a.config.WindowX)
		y = uintptr(a.config.WindowY)
	}
	return x, y, uintptr(width), uintptr(height)
}

func createChildControl(parent hwnd, className, title string, style uintptr, exStyle uintptr, menu hmenu) (hwnd, error) {
	instance, err := getModuleHandle()
	if err != nil {
		return 0, err
	}

	classNamePtr := utf16Ptr(className)
	titlePtr := utf16Ptr(title)
	handle, _, createErr := procCreateWindowEx.Call(
		exStyle,
		uintptr(unsafe.Pointer(classNamePtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		style,
		0,
		0,
		100,
		24,
		uintptr(parent),
		uintptr(menu),
		uintptr(instance),
		0,
	)
	if handle == 0 {
		return 0, fmt.Errorf("创建控件失败 %s：%v", className, createErr)
	}
	return hwnd(handle), nil
}

func initTheme() {
	appTheme = theme{
		windowColor:      rgb(30, 30, 30),
		surfaceColor:     rgb(60, 60, 60),
		textColor:        rgb(204, 204, 204),
		borderColor:      rgb(69, 69, 69),
		buttonColor:      rgb(14, 99, 156),
		buttonHoverColor: rgb(17, 119, 187),
		buttonDisabled:   rgb(90, 93, 94),
		progressTrack:    rgb(51, 51, 51),
		progressFill:     rgb(14, 99, 156),
	}
	appTheme.windowBrush = createSolidBrush(appTheme.windowColor)
	appTheme.surfaceBrush = createSolidBrush(appTheme.surfaceColor)
	appTheme.borderBrush = createSolidBrush(appTheme.borderColor)
	appTheme.buttonBrush = createSolidBrush(appTheme.buttonColor)
	appTheme.buttonHoverBrush = createSolidBrush(appTheme.buttonHoverColor)
	appTheme.buttonDisabledBr = createSolidBrush(appTheme.buttonDisabled)
	appTheme.progressTrackBr = createSolidBrush(appTheme.progressTrack)
	appTheme.progressFillBr = createSolidBrush(appTheme.progressFill)
}

func loadAppIcon(instance hinstance, width, height int) (uintptr, error) {
	handle, _, err := procLoadImage.Call(
		uintptr(instance),
		appIconID,
		imageIcon,
		uintptr(width),
		uintptr(height),
		lrDefaultColor,
	)
	if handle == 0 {
		return 0, fmt.Errorf("加载程序图标失败：%v", err)
	}
	return handle, nil
}

func getModuleHandle() (hinstance, error) {
	handle, _, err := procGetModuleHandle.Call(0)
	if handle == 0 {
		return 0, fmt.Errorf("获取模块句柄失败：%v", err)
	}
	return hinstance(handle), nil
}

func applyDarkTitleBar(window hwnd) {
	value := int32(1)
	procDwmSetWindowAttribute.Call(uintptr(window), dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value))
	captionColor := appTheme.windowColor
	textColor := appTheme.textColor
	borderColor := appTheme.borderColor
	procDwmSetWindowAttribute.Call(uintptr(window), dwmwaCaptionColor, uintptr(unsafe.Pointer(&captionColor)), unsafe.Sizeof(captionColor))
	procDwmSetWindowAttribute.Call(uintptr(window), dwmwaTextColor, uintptr(unsafe.Pointer(&textColor)), unsafe.Sizeof(textColor))
	procDwmSetWindowAttribute.Call(uintptr(window), dwmwaBorderColor, uintptr(unsafe.Pointer(&borderColor)), unsafe.Sizeof(borderColor))
}

func enableWindow(window hwnd, enabled bool) {
	value := uintptr(0)
	if enabled {
		value = 1
	}
	procEnableWindow.Call(uintptr(window), value)
}

func moveWindow(window hwnd, x, y, width, height int) {
	procMoveWindow.Call(uintptr(window), uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
}

func invalidate(window hwnd) {
	procInvalidateRect.Call(uintptr(window), 0, 1)
}

func setWindowText(window hwnd, text string) {
	ptr := utf16Ptr(text)
	procSetWindowText.Call(uintptr(window), uintptr(unsafe.Pointer(ptr)))
}

func currentThreadID() uint32 {
	threadID, _, _ := procGetCurrentThreadID.Call()
	return uint32(threadID)
}

func groupInputProc(window hwnd, message uint32, wParam, lParam uintptr) uintptr {
	if currentApp == nil || currentApp.groupInput == 0 || window != currentApp.groupInput || currentApp.groupInputProc == 0 {
		ret, _, _ := procDefWindowProc.Call(uintptr(window), uintptr(message), wParam, lParam)
		return ret
	}

	switch message {
	case wmSetFocus:
		diaglog.Logf("group input focus")
		ret := callWindowProc(currentApp.groupInputProc, window, message, wParam, lParam)
		sendMessage(window, emSetSel, 0, ^uintptr(0))
		return ret
	case wmLButtonDown, wmLButtonDblClk:
		atomic.StoreUint32(&currentApp.dragLogged, 0)
		diaglog.Logf("group input click message=%#x", message)
		procSetFocus.Call(uintptr(window))
		sendMessage(window, emSetSel, 0, ^uintptr(0))
		return 0
	case wmMouseMove:
		if wParam&0x0001 != 0 {
			if atomic.CompareAndSwapUint32(&currentApp.dragLogged, 0, 1) {
				diaglog.Logf("group input drag suppressed")
			}
			return 0
		}
	}

	return callWindowProc(currentApp.groupInputProc, window, message, wParam, lParam)
}

func getWindowText(window hwnd) string {
	length, _, _ := procGetWindowTextLength.Call(uintptr(window))
	buffer := make([]uint16, length+1)
	sendMessage(window, wmGetText, uintptr(len(buffer)), uintptr(unsafe.Pointer(&buffer[0])))
	return syscall.UTF16ToString(buffer)
}

func sendMessage(window hwnd, message uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procSendMessage.Call(uintptr(window), uintptr(message), wParam, lParam)
	return ret
}

func callWindowProc(proc uintptr, window hwnd, message uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procCallWindowProc.Call(proc, uintptr(window), uintptr(message), wParam, lParam)
	return ret
}

func showMessage(window hwnd, text string, flags uintptr) {
	message := utf16Ptr(text)
	title := utf16Ptr(appTitle)
	procMessageBox.Call(uintptr(window), uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), flags)
}

func utf16Ptr(text string) *uint16 {
	ptr, _ := syscall.UTF16PtrFromString(text)
	return ptr
}

func createSolidBrush(color uint32) uintptr {
	brush, _, _ := procCreateSolidBrush.Call(uintptr(color))
	return brush
}

func createFont(height int32, weight int32, face string) uintptr {
	name := utf16Ptr(face)
	font, _, _ := procCreateFont.Call(
		uintptr(height),
		0,
		0,
		0,
		uintptr(weight),
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		0,
		uintptr(unsafe.Pointer(name)),
	)
	return font
}

func drawButton(hdc uintptr, button rect, disabled bool, pressed bool) {
	brush := appTheme.buttonBrush
	if disabled {
		brush = appTheme.buttonDisabledBr
	} else if pressed {
		brush = appTheme.buttonHoverBrush
	}

	procFrameRect.Call(hdc, uintptr(unsafe.Pointer(&button)), appTheme.borderBrush)
	inner := shrinkRect(button, 1)
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&inner)), brush)
	procSetBkMode.Call(hdc, transparentMode)
	procSetTextColor.Call(hdc, uintptr(appTheme.textColor))
	procDrawText.Call(
		hdc,
		uintptr(unsafe.Pointer(utf16Ptr("开始"))),
		^uintptr(0),
		uintptr(unsafe.Pointer(&inner)),
		drawTextCenter|drawTextVCenter|drawTextSingleLine,
	)
}

func drawProgress(hdc uintptr, bar rect, percent int) {
	procFrameRect.Call(hdc, uintptr(unsafe.Pointer(&bar)), appTheme.borderBrush)
	track := shrinkRect(bar, 1)
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&track)), appTheme.progressTrackBr)

	if percent <= 0 {
		return
	}

	fill := track
	width := int(track.Right-track.Left) * percent / 100
	if width < 2 {
		width = 2
	}
	fill.Right = fill.Left + int32(width)
	if fill.Right > track.Right {
		fill.Right = track.Right
	}
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&fill)), appTheme.progressFillBr)
}

func shrinkRect(r rect, size int32) rect {
	return rect{
		Left:   r.Left + size,
		Top:    r.Top + size,
		Right:  r.Right - size,
		Bottom: r.Bottom - size,
	}
}

func coordsFromLParam(lParam uintptr) (int32, int32) {
	x := int16(lParam & 0xffff)
	y := int16((lParam >> 16) & 0xffff)
	return int32(x), int32(y)
}

func pointInRect(x, y int32, r rect) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

func rgb(r, g, b byte) uint32 {
	return uint32(r) | uint32(g)<<8 | uint32(b)<<16
}
