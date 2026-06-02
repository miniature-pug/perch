package pty

import (
	"io"
	"sync"
	"testing"
	"time"
)

func TestBridge_WriteForwardsToPty(t *testing.T) {
	pr, pw := io.Pipe()
	b := &Bridge{ptyFile: pw}

	go func() { _, _ = b.Write([]byte("ls\r")) }()

	buf := make([]byte, 3)
	if _, err := io.ReadFull(pr, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if string(buf) != "ls\r" {
		t.Fatalf("pty received %q, want %q", buf, "ls\r")
	}
}

func TestBridge_ResizeCallsSetter(t *testing.T) {
	var gotCols, gotRows uint16
	b := &Bridge{
		setsize: func(cols, rows uint16) error {
			gotCols, gotRows = cols, rows
			return nil
		},
	}
	if err := b.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if gotCols != 120 || gotRows != 40 {
		t.Fatalf("setter got cols=%d rows=%d, want 120/40", gotCols, gotRows)
	}
}

func TestPumpReader_BatchesChunksAsIntSlices(t *testing.T) {
	pr, pw := io.Pipe()

	var mu sync.Mutex
	var got [][]int
	emit := func(_ string, data ...any) {
		mu.Lock()
		defer mu.Unlock()
		if len(data) == 1 {
			if b, ok := data[0].([]int); ok {
				cp := make([]int, len(b))
				copy(cp, b)
				got = append(got, cp)
			}
		}
	}

	done := make(chan struct{})
	go func() {
		pumpReader(pr, "pty-data:t1", emit, 4096, 8*time.Millisecond)
		close(done)
	}()

	_, _ = pw.Write([]byte("hello "))
	_, _ = pw.Write([]byte("world"))
	_ = pw.Close()
	<-done

	mu.Lock()
	defer mu.Unlock()
	var total []byte
	for _, c := range got {
		for _, v := range c {
			total = append(total, byte(v))
		}
	}
	if string(total) != "hello world" {
		t.Fatalf("reassembled bytes = %q, want %q", total, "hello world")
	}
	for i, c := range got {
		if len(c) > 4096 {
			t.Fatalf("chunk %d exceeded max size: %d", i, len(c))
		}
	}
}
