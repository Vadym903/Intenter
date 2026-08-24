package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxMessageBytes caps one framed message at 1 MiB (§10.2). A larger message is
// rejected rather than buffered.
const MaxMessageBytes = 1 << 20

// ErrMessageTooLarge is returned when a peer sends more than MaxMessageBytes.
var ErrMessageTooLarge = errors.New("ipc: message exceeds 1 MiB")

// Framer reads and writes the one-JSON-object-per-line framing used on both
// transports: exactly one request and one response per connection (§10.2).
type Framer struct {
	reader *bufio.Reader
	writer io.Writer
}

// NewFramer wraps a connection.
func NewFramer(rw io.ReadWriter) *Framer {
	return &Framer{
		reader: bufio.NewReaderSize(io.LimitReader(rw, MaxMessageBytes+1), 64*1024),
		writer: rw,
	}
}

// Read decodes exactly one message.
func (f *Framer) Read(target any) error {
	line, err := f.reader.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return io.EOF
		}
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("ipc: read message: %w", err)
		}
		// A final message without a trailing newline is still usable, unless it
		// hit the size cap.
		if len(line) > MaxMessageBytes {
			return ErrMessageTooLarge
		}
	}
	if len(line) > MaxMessageBytes {
		return ErrMessageTooLarge
	}
	if err := json.Unmarshal(trimNewline(line), target); err != nil {
		return fmt.Errorf("ipc: decode message: %w", err)
	}
	return nil
}

// Write encodes exactly one message followed by a newline.
func (f *Framer) Write(message any) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("ipc: encode message: %w", err)
	}
	if len(encoded)+1 > MaxMessageBytes {
		return ErrMessageTooLarge
	}
	encoded = append(encoded, '\n')
	if _, err := f.writer.Write(encoded); err != nil {
		return fmt.Errorf("ipc: write message: %w", err)
	}
	return nil
}

func trimNewline(line []byte) []byte {
	for len(line) > 0 && (line[len(line)-1] == '\n' || line[len(line)-1] == '\r') {
		line = line[:len(line)-1]
	}
	return line
}
