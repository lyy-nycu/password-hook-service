package sensitiveio

import (
	"bytes"
	"io"
	"testing"
)

func TestReadAllZerosPreviousBufferOnGrowth(t *testing.T) {
	t.Parallel()

	first := bytes.Repeat([]byte("a"), 512)
	second := []byte("second-password-chunk")
	reader := &observingReader{chunks: [][]byte{first, second}}

	body, err := ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if got, want := string(body), string(append(append([]byte(nil), first...), second...)); got != want {
		t.Fatalf("body length/content mismatch")
	}
	if len(reader.observed) < 2 {
		t.Fatalf("observed reads = %d, want at least 2", len(reader.observed))
	}
	assertZeroedBytes(t, reader.observed[0], "first read buffer after growth")
	for i, b := range body {
		if b == 0 {
			t.Fatalf("body byte %d was zeroed before caller ownership", i)
		}
	}
}

func TestReadAllReturnsPartialDataOnReadError(t *testing.T) {
	t.Parallel()

	reader := &observingReader{chunks: [][]byte{[]byte("partial-password")}, err: io.ErrUnexpectedEOF}

	body, err := ReadAll(reader)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("ReadAll error = %v, want unexpected EOF", err)
	}
	if got := string(body); got != "partial-password" {
		t.Fatalf("body = %q, want partial-password", got)
	}
}

type observingReader struct {
	chunks   [][]byte
	err      error
	observed [][]byte
}

func (r *observingReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	n := copy(p, chunk)
	r.observed = append(r.observed, p[:n])
	if len(r.chunks) == 0 && r.err != nil {
		return n, r.err
	}
	return n, nil
}

func assertZeroedBytes(t *testing.T, buf []byte, context string) {
	t.Helper()
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("%s byte %d = %d, want 0", context, i, b)
		}
	}
}
