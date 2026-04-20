package cursor

import (
	"fmt"
	"sync"
	"testing"
)

func TestCursorStderrBufferWrite(t *testing.T) {
	buf := &stderrBuffer{}

	n, err := buf.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write err = %v", err)
	}
	if n != 5 {
		t.Errorf("Write n = %d, want 5", n)
	}

	if _, err := buf.Write([]byte(" world")); err != nil {
		t.Fatalf("Write err = %v", err)
	}

	if got := buf.String(); got != "hello world" {
		t.Errorf("String() = %q, want %q", got, "hello world")
	}
}

func TestCursorStderrBufferConcurrentWrite(t *testing.T) {
	buf := &stderrBuffer{}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				fmt.Fprintf(buf, "line %d-%d\n", n, j)
			}
		}(i)
	}
	wg.Wait()

	if buf.String() == "" {
		t.Error("buffer should contain content after concurrent writes")
	}
}
