package jsonrpc2

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// MaxMessageSize limits the size of a single incoming message body.
const MaxMessageSize = 256 << 20

// readMessage reads one Content-Length framed message body.
func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && line != "" {
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
	if length > MaxMessageSize {
		return nil, fmt.Errorf("jsonrpc2: message of %d bytes exceeds limit", length)
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
