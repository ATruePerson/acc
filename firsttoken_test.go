package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

// fast path: data is already available, so awaitFirstByte returns a reader that
// replays every byte (nothing lost) and does not report a timeout.
func TestAwaitFirstByteFast(t *testing.T) {
	src := strings.NewReader("data: {\"hello\":1}\n\n")
	r, timedOut := awaitFirstByte(src, 2*time.Second)
	if timedOut {
		t.Fatal("did not expect timeout for an immediately-readable source")
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read replayed reader: %v", err)
	}
	if string(got) != "data: {\"hello\":1}\n\n" {
		t.Fatalf("first byte lost; got %q", string(got))
	}
}

// slow path: the source never produces a byte, so awaitFirstByte must report a
// timeout once the deadline passes.
func TestAwaitFirstByteTimeout(t *testing.T) {
	pr, pw := io.Pipe() // reads block until something is written; nothing is
	defer pw.Close()

	start := time.Now()
	r, timedOut := awaitFirstByte(pr, 50*time.Millisecond)
	if !timedOut {
		t.Fatal("expected timeout when source produces no byte")
	}
	if r != nil {
		t.Fatal("expected nil reader on timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("returned too slowly (%v); should fire near the deadline", elapsed)
	}
	pr.Close() // unblock the pending read goroutine
}

// a byte that arrives just under the deadline should still count as responding.
func TestAwaitFirstByteJustInTime(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		time.Sleep(20 * time.Millisecond)
		pw.Write([]byte("x"))
		pw.Close()
	}()
	r, timedOut := awaitFirstByte(pr, 500*time.Millisecond)
	if timedOut {
		t.Fatal("byte arrived before deadline; should not time out")
	}
	got, _ := io.ReadAll(r)
	if string(got) != "x" {
		t.Fatalf("got %q, want %q", string(got), "x")
	}
}
