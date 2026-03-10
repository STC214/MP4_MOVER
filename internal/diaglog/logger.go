package diaglog

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const (
	logFileName   = "VidoWallpaperNumbers.log"
	logBufferSize = 2048
)

type logger struct {
	path    string
	file    *os.File
	writer  *bufio.Writer
	entries chan string
	done    chan struct{}
	dropped atomic.Uint64
	closed  atomic.Uint32
}

var (
	mu     sync.RWMutex
	active *logger
)

func Init(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("log dir is empty")
	}

	path := filepath.Join(dir, logFileName)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}

	l := &logger{
		path:    path,
		file:    file,
		writer:  bufio.NewWriterSize(file, 64*1024),
		entries: make(chan string, logBufferSize),
		done:    make(chan struct{}),
	}

	go l.run()

	mu.Lock()
	if active != nil {
		_ = active.close()
	}
	active = l
	mu.Unlock()

	Logf("========== session start %s ==========", time.Now().Format(time.RFC3339))
	return path, nil
}

func Close() error {
	mu.Lock()
	l := active
	active = nil
	mu.Unlock()
	if l == nil {
		return nil
	}
	return l.close()
}

func Path() string {
	mu.RLock()
	defer mu.RUnlock()
	if active == nil {
		return ""
	}
	return active.path
}

func Logf(format string, args ...any) {
	logLine(fmt.Sprintf(format, args...))
}

func logLine(message string) {
	mu.RLock()
	l := active
	mu.RUnlock()
	if l == nil || l.closed.Load() != 0 {
		return
	}

	line := fmt.Sprintf("%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), message)
	select {
	case l.entries <- line:
	default:
		l.dropped.Add(1)
	}
}

func (l *logger) run() {
	for entry := range l.entries {
		if dropped := l.dropped.Swap(0); dropped > 0 {
			_, _ = l.writer.WriteString(fmt.Sprintf("%s [diaglog] dropped %d log lines due to backpressure\n", time.Now().Format("2006-01-02 15:04:05.000"), dropped))
		}
		_, _ = l.writer.WriteString(entry)
		_ = l.writer.Flush()
	}

	if dropped := l.dropped.Swap(0); dropped > 0 {
		_, _ = l.writer.WriteString(fmt.Sprintf("%s [diaglog] dropped %d log lines due to backpressure\n", time.Now().Format("2006-01-02 15:04:05.000"), dropped))
	}
	_ = l.writer.Flush()
	_ = l.file.Close()
	close(l.done)
}

func (l *logger) close() error {
	if !l.closed.CompareAndSwap(0, 1) {
		<-l.done
		return nil
	}
	close(l.entries)
	<-l.done
	return nil
}
