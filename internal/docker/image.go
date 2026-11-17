package docker

import (
	"context"
	"fmt"
	"os/exec"
	"path"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/interp"
)

// DefaultRemote is the container directory the project is mounted at when
// an image interpreter names none.
const DefaultRemote = "/app"

// ImageIDPrefix starts the ID of every image interpreter.
const ImageIDPrefix = "docker:"

// Mapping is a host/container path pair of an image interpreter.
type Mapping struct {
	Host   string `json:"host"`
	Remote string `json:"remote"`
}

// ImageInterpreter runs php in a one-off container of an image:
//
//	docker run --rm -i -v HOST:REMOTE -w REMOTE --entrypoint php IMAGE args…
//
// It implements interp.Interpreter.
type ImageInterpreter struct {
	// Image is the image reference, e.g. "php:8.3-cli".
	Image string
	// Remote is the container directory of the project (DefaultRemote if
	// empty).
	Remote string
	// HostDir is the project directory mounted at Remote; empty mounts
	// nothing.
	HostDir string
	// PHP is the php binary inside the image; empty means "php".
	PHP string
	// Client is the docker CLI to use (binary, context, environment).
	Client Client
	// NoPull passes --pull never: the image must exist locally. Automatic
	// probes use it so that no image is downloaded without a user action.
	NoPull bool
	// PHPVersion caches the last successful Version result.
	PHPVersion string
}

var _ interp.Interpreter = (*ImageInterpreter)(nil)

// ImageID returns the interpreter ID of image.
func ImageID(image string) string { return ImageIDPrefix + image }

// ParseImageSpec splits a setting entry "IMAGE" or "IMAGE=REMOTE".
func ParseImageSpec(spec string) (image, remote string, err error) {
	image, remote, _ = strings.Cut(strings.TrimSpace(spec), "=")
	image, remote = strings.TrimSpace(image), strings.TrimSpace(remote)
	if image == "" || strings.ContainsAny(image, " \t") {
		return "", "", fmt.Errorf("docker: invalid image %q", spec)
	}
	if remote == "" {
		remote = DefaultRemote
	}
	if !strings.HasPrefix(remote, "/") {
		return "", "", fmt.Errorf("docker: remote path %q of image %s must be absolute", remote, image)
	}
	return image, path.Clean(remote), nil
}

// FormatImageSpec is the inverse of ParseImageSpec; the default remote is
// omitted.
func FormatImageSpec(image, remote string) string {
	if remote == "" || remote == DefaultRemote {
		return image
	}
	return image + "=" + remote
}

// NewImageInterpreter returns an interpreter for image with the project
// directory hostDir mounted at remote.
func NewImageInterpreter(image, remote, hostDir string, c Client) *ImageInterpreter {
	return &ImageInterpreter{Image: image, Remote: remote, HostDir: hostDir, Client: c}
}

// WithoutPull returns a copy that never pulls the image (--pull never).
func (d *ImageInterpreter) WithoutPull() *ImageInterpreter {
	cp := *d
	cp.NoPull = true
	return &cp
}

// ID implements interp.Interpreter.
func (d *ImageInterpreter) ID() string { return ImageID(d.Image) }

// Label implements interp.Interpreter.
func (d *ImageInterpreter) Label() string {
	if d.PHPVersion != "" {
		return fmt.Sprintf("PHP %s (docker %s)", d.PHPVersion, d.Image)
	}
	return fmt.Sprintf("PHP (docker %s)", d.Image)
}

// Kind implements interp.Interpreter.
func (d *ImageInterpreter) Kind() interp.Kind { return interp.KindDocker }

// RemoteDir returns the container directory of the project.
func (d *ImageInterpreter) RemoteDir() string {
	if d.Remote == "" {
		return DefaultRemote
	}
	return d.Remote
}

// Mappings returns the host/container mapping of the project volume.
func (d *ImageInterpreter) Mappings() []Mapping {
	if d.HostDir == "" {
		return nil
	}
	return []Mapping{{Host: d.HostDir, Remote: d.RemoteDir()}}
}

// RunArgs returns the docker arguments (without global options) that run
// php with args.
func (d *ImageInterpreter) RunArgs(args ...string) []string {
	php := d.PHP
	if php == "" {
		php = "php"
	}
	a := []string{"run", "--rm", "-i"}
	if d.NoPull {
		a = append(a, "--pull", "never")
	}
	if d.HostDir != "" {
		a = append(a, "-v", d.HostDir+":"+d.RemoteDir(), "-w", d.RemoteDir())
	}
	a = append(a, "--entrypoint", php, d.Image)
	return append(a, args...)
}

// ImageExists reports whether image is available in the engine of c
// (`docker image ls`; never pulls). An unreachable engine is an error.
func (c Client) ImageExists(ctx context.Context, image string) (bool, error) {
	out, err := c.Output(ctx, "image", "ls", "--quiet", "--no-trunc", image)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// Version implements interp.Interpreter.
func (d *ImageInterpreter) Version(ctx context.Context) (string, error) {
	v, err := interp.QueryVersion(ctx, d)
	if err != nil {
		return "", err
	}
	d.PHPVersion = v
	return v, nil
}

// Modules implements interp.Interpreter.
func (d *ImageInterpreter) Modules(ctx context.Context) ([]string, error) {
	return interp.QueryModules(ctx, d)
}

// Ini implements interp.Interpreter.
func (d *ImageInterpreter) Ini(ctx context.Context) (interp.IniValues, error) {
	return interp.QueryIni(ctx, d)
}

// Command implements interp.Interpreter.
func (d *ImageInterpreter) Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	cmd := d.Client.Command(ctx, d.RunArgs(args...)...)
	if d.HostDir != "" {
		cmd.Dir = d.HostDir
	}
	return cmd, nil
}
