package cursorcloud

import (
	"bytes"
	"testing"
)

func TestLimitedBufferCapsStoredOutput(t *testing.T) {
	buffer := newLimitedBuffer(4)
	written, err := buffer.Write([]byte("abcdef"))
	if err != nil || written != 6 {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if !buffer.exceeded || !bytes.Equal(buffer.Bytes(), []byte("abcd")) {
		t.Fatalf("limited buffer = %q, exceeded = %v", buffer.Bytes(), buffer.exceeded)
	}
}
