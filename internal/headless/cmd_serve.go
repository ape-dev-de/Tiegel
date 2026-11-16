package headless

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ape-dev-de/tiegel/internal/api"
	"github.com/ape-dev-de/tiegel/internal/api/server"
)

func init() {
	for name, takesValue := range map[string]bool{
		"listen": true, "token-file": true, "token": true, "allow-remote": false,
	} {
		flagSpec[name] = takesValue
	}
}

// ListenInfo is the line `serve --listen` prints on stdout once it accepts
// connections, so that a parent process can pick up the address and token
// file.
type ListenInfo struct {
	Listen    string `json:"listen"`
	TokenFile string `json:"tokenFile,omitempty"`
	PID       int    `json:"pid"`
}

// runListen serves the API on a socket until the context ends (SIGINT,
// SIGTERM). Token: --token, else $TIEGEL_TOKEN, else a random one written to
// --token-file (default <config-dir>/serve.token).
func runListen(c *cli, opts api.ServeOptions) error {
	cfg := server.Config{
		Addr:        c.p.value("listen"),
		AllowRemote: c.p.has("allow-remote"),
		Token:       c.p.value("token"),
		TokenFile:   c.p.value("token-file"),
		Serve:       opts,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(c.env.Stderr, "tiegel: "+format+"\n", args...)
		},
	}
	if cfg.Token == "" {
		cfg.Token = c.env.Getenv(server.TokenEnv)
	}
	if cfg.Token == "" && cfg.TokenFile == "" {
		cfg.TokenFile = filepath.Join(c.app.ConfigDir(), "serve.token")
	}
	s, err := server.Listen(cfg)
	if err != nil {
		return err
	}
	info := ListenInfo{Listen: s.Addr(), TokenFile: s.TokenFile(), PID: os.Getpid()}
	if err := json.NewEncoder(c.env.Stdout).Encode(info); err != nil {
		_ = s.Close()
		return err
	}
	if info.TokenFile != "" {
		fmt.Fprintf(c.env.Stderr, "tiegel: listening on %s (token in %s)\n", info.Listen, info.TokenFile)
	} else {
		fmt.Fprintf(c.env.Stderr, "tiegel: listening on %s\n", info.Listen)
	}
	err = s.Serve(c.ctx, c.app)
	fmt.Fprintln(c.env.Stderr, "tiegel: server stopped")
	return err
}
