package sensitiveio

import (
	"io"

	"github.com/nycu/password-hook-service/internal/passwordcrypto"
)

func ReadAll(r io.Reader) ([]byte, error) {
	buf := make([]byte, 0, 512)
	for {
		if len(buf) == cap(buf) {
			buf = growZeroing(buf, len(buf)+1)
		}

		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return buf, err
		}

		if cap(buf)-len(buf) < cap(buf)/16 {
			buf = growZeroing(buf, len(buf)+1)
		}
	}
}

func growZeroing(buf []byte, minCapacity int) []byte {
	newCapacity := cap(buf) + cap(buf)/2
	if newCapacity < minCapacity {
		newCapacity = minCapacity
	}
	if newCapacity == 0 {
		newCapacity = 512
	}
	next := make([]byte, len(buf), newCapacity)
	copy(next, buf)
	passwordcrypto.ZeroBytes(buf[:cap(buf)])
	return next
}
