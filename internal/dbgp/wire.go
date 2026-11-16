package dbgp

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxPacketSize limits the size of a single incoming packet.
const maxPacketSize = 64 << 20

// readPacket reads one engine packet of the form "length\0xml\0".
func readPacket(r *bufio.Reader) ([]byte, error) {
	head, err := r.ReadString(0)
	if err != nil {
		return nil, err
	}
	head = strings.TrimSpace(head[:len(head)-1])
	n, err := strconv.Atoi(head)
	if err != nil || n < 0 || n > maxPacketSize {
		return nil, fmt.Errorf("dbgp: invalid packet length %q", head)
	}
	buf := make([]byte, n+1)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("dbgp: read packet body: %w", err)
	}
	if buf[n] != 0 {
		return nil, errors.New("dbgp: packet is not NUL-terminated")
	}
	return buf[:n], nil
}

// encodeCommand builds an IDE command "name -i txid args -- base64(data)\0".
// args are flag/value pairs, e.g. []string{"-n", "max_depth", "-v", "1"}.
func encodeCommand(name string, txid int, args []string, data []byte) ([]byte, error) {
	if name == "" || strings.ContainsAny(name, " \t\x00\"") {
		return nil, fmt.Errorf("dbgp: invalid command name %q", name)
	}
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("dbgp: %s: odd number of arguments", name)
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteString(" -i ")
	b.WriteString(strconv.Itoa(txid))
	for i := 0; i < len(args); i += 2 {
		flag, val := args[i], args[i+1]
		if len(flag) != 2 || flag[0] != '-' || flag == "-i" || flag == "--" {
			return nil, fmt.Errorf("dbgp: %s: invalid flag %q", name, flag)
		}
		if strings.IndexByte(val, 0) >= 0 {
			return nil, fmt.Errorf("dbgp: %s: argument %s contains NUL", name, flag)
		}
		b.WriteByte(' ')
		b.WriteString(flag)
		b.WriteByte(' ')
		b.WriteString(quoteArg(val))
	}
	if data != nil {
		b.WriteString(" -- ")
		b.WriteString(base64.StdEncoding.EncodeToString(data))
	}
	b.WriteByte(0)
	return []byte(b.String()), nil
}

// quoteArg quotes an argument value if it contains characters that would
// otherwise break the command line. Inside quotes, backslash and double quote
// are escaped with a backslash (Xdebug applies stripcslashes).
func quoteArg(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t\"\\") {
		return v
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(v) + `"`
}
