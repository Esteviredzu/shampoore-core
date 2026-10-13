package logging

import (
	"context"
	"fmt"
	"os"
	"sync"
)

type Logger struct {
	mu     sync.Mutex
	prefix string
	debug  bool
}

func New(prefix string, debug bool) *Logger {
	return &Logger{prefix: prefix, debug: debug}
}

func (l *Logger) emit(level string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(os.Stderr, "[%s] %s: %s\n",
		level, l.prefix, fmt.Sprint(args...))
}

func (l *Logger) Trace(args ...any) {
	if l.debug {
		l.emit("TRACE", args...)
	}
}

func (l *Logger) Debug(args ...any) {
	if l.debug {
		l.emit("DEBUG", args...)
	}
}

func (l *Logger) Info(args ...any)  { l.emit("INFO", args...) }
func (l *Logger) Warn(args ...any)  { l.emit("WARN", args...) }
func (l *Logger) Error(args ...any) { l.emit("ERROR", args...) }

func (l *Logger) Fatal(args ...any) {
	l.emit("FATAL", args...)
	os.Exit(1)
}

func (l *Logger) Panic(args ...any) {
	s := fmt.Sprint(args...)
	l.emit("PANIC", s)
	panic(s)
}

// Context-варианты: контекст игнорируем, но интерфейс требует.
func (l *Logger) TraceContext(ctx context.Context, args ...any) { l.Trace(args...) }
func (l *Logger) DebugContext(ctx context.Context, args ...any) { l.Debug(args...) }
func (l *Logger) InfoContext(ctx context.Context, args ...any)  { l.Info(args...) }
func (l *Logger) WarnContext(ctx context.Context, args ...any)  { l.Warn(args...) }
func (l *Logger) ErrorContext(ctx context.Context, args ...any) { l.Error(args...) }
func (l *Logger) FatalContext(ctx context.Context, args ...any) { l.Fatal(args...) }
func (l *Logger) PanicContext(ctx context.Context, args ...any) { l.Panic(args...) }