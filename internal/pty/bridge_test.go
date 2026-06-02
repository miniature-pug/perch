package pty

import (
	"io"
	"sync"
	"testing"
	"time"
)

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
