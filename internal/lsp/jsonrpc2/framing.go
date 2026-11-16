package jsonrpc2

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// MaxMessageSize limits the size of a single incoming message body.
const MaxMessageSize = 256 << 20

// ErrMessageTooLarge is returned by ReadMessageLimit when a message (or a
// header line) exceeds the limit.
var ErrMessageTooLarge = errors.New("jsonrpc2: message exceeds size limit")

// LimitedReader is implemented by HeaderFraming and LineFraming: it reads
// like ReadMessage but rejects messages larger than max bytes before
// buffering them completely (header lines count as well). Servers use it to
// keep unauthenticated peers from making them allocate large buffers.
type LimitedReader interface {
	ReadMessageLimit(r *bufio.Reader, max int) ([]byte, error)
}

// readLimitedLine reads one line (without terminator handling) of at most
// max bytes.
func readLimitedLine(r *bufio.Reader, max int) (string, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				err = io.ErrUnexpectedEOF
			}
			return "", err
		}
		buf = append(buf, chunk...)
		if len(buf) > max {
			return "", fmt.Errorf("%w (%d bytes)", ErrMessageTooLarge, max)
		}
		if !isPrefix {
			return string(buf), nil
		}
	}
}

// readMessage reads one Content-Length framed message body of at most max
// bytes.
func readMessage(r *bufio.Reader, max int) ([]byte, error) {
	length := -1
	for started := false; ; started = true {
		line, err := readLimitedLine(r, max)
		if err != nil {
			if err == io.EOF && started {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("jsonrpc2: malformed header %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("jsonrpc2: invalid Content-Length %q", value)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("jsonrpc2: missing Content-Length header")
	}
	if length > max {
		return nil, fmt.Errorf("%w: %d > %d bytes", ErrMessageTooLarge, length, max)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, fmt.Errorf("jsonrpc2: reading body: %w", err)
	}
	return body, nil
}

// writeMessage writes body with a Content-Length header.
func writeMessage(w io.Writer, body []byte) error {
	buf := make([]byte, 0, len(body)+32)
	buf = append(buf, "Content-Length: "...)
	buf = strconv.AppendInt(buf, int64(len(body)), 10)
	buf = append(buf, "\r\n\r\n"...)
	buf = append(buf, body...)
	_, err := w.Write(buf)
	return err
}

// Framing splits a byte stream into JSON-RPC message bodies. Conn uses
// HeaderFraming (LSP style) unless NewConnFraming is given another one.
// ReadMessage is only called from the connection's read goroutine;
// WriteMessage calls are serialised by the connection.
type Framing interface {
	// ReadMessage returns the next message body. At the end of the stream
	// it returns io.EOF.
	ReadMessage(r *bufio.Reader) ([]byte, error)
	// WriteMessage writes one message body including its framing.
	WriteMessage(w io.Writer, body []byte) error
}

// HeaderFraming is the Content-Length header framing of the Language Server
// Protocol.
var HeaderFraming Framing = headerFraming{}

// LineFraming frames every message as one line of JSON terminated by "\n"
// (newline-delimited JSON, NDJSON). JSON encoders never emit raw newlines
// inside a value, so a message always fits on one line. Empty lines are
// skipped when reading.
var LineFraming Framing = lineFraming{}

type headerFraming struct{}

func (headerFraming) ReadMessage(r *bufio.Reader) ([]byte, error) {
	return readMessage(r, MaxMessageSize)
}

// ReadMessageLimit implements LimitedReader.
func (headerFraming) ReadMessageLimit(r *bufio.Reader, max int) ([]byte, error) {
	return readMessage(r, min(max, MaxMessageSize))
}
func (headerFraming) WriteMessage(w io.Writer, body []byte) error { return writeMessage(w, body) }

type lineFraming struct{}

func (lineFraming) ReadMessage(r *bufio.Reader) ([]byte, error) {
	return readLine(r, MaxMessageSize)
}

// ReadMessageLimit implements LimitedReader.
func (lineFraming) ReadMessageLimit(r *bufio.Reader, max int) ([]byte, error) {
	return readLine(r, min(max, MaxMessageSize))
}

func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				return buf, nil
			}
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > max {
			return nil, fmt.Errorf("%w (%d bytes)", ErrMessageTooLarge, max)
		}
		if isPrefix {
			continue
		}
		if len(strings.TrimSpace(string(buf))) == 0 {
			buf = buf[:0]
			continue
		}
		return buf, nil
	}
}

func (lineFraming) WriteMessage(w io.Writer, body []byte) error {
	buf := make([]byte, 0, len(body)+1)
	buf = append(buf, body...)
	buf = append(buf, '\n')
	_, err := w.Write(buf)
	return err
}
