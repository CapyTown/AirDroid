package scrcpy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/CapyTown/AirDroid/internal/proc"
)

// Server is a scrcpy-server file and the version it was built as, which
// the server checks against the version AirDroid announces.
type Server struct {
	Path    string // the scrcpy-server file pushed to the device
	Version string // as "5.0.1"; "" when unknown
	Binary  string // the scrcpy executable it came with, if any
}

var scrcpyVersionRe = regexp.MustCompile(`(?m)^scrcpy v?(\d+\.\d+(?:\.\d+)?)`)

// ParseScrcpyVersion reads the output of `scrcpy --version`.
func ParseScrcpyVersion(out string) string {
	if m := scrcpyVersionRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

var serverFileVersionRe = regexp.MustCompile(`scrcpy-server-v?(\d+\.\d+(?:\.\d+)?)`)

// ResolveServer finds the scrcpy-server for path, which is either a scrcpy
// executable, as Homebrew or scrcpy's releases install it, or a
// scrcpy-server file itself. The version comes from the file next to it
// (scrcpy-server.version, which AirDroid's bundle carries), from running the
// executable, or from the file's name.
func ResolveServer(ctx context.Context, path string) (Server, error) {
	if path == "" {
		return Server{}, errors.New("no scrcpy configured")
	}
	if p, err := filepath.EvalSymlinks(path); err == nil {
		path = p
	}
	st, err := os.Stat(path)
	if err != nil {
		return Server{}, err
	}
	var s Server
	if st.IsDir() {
		dir := path
		path = filepath.Join(dir, "scrcpy")
		for _, name := range []string{"scrcpy-server", "scrcpy.exe"} {
			if fileExists(filepath.Join(dir, name)) {
				path = filepath.Join(dir, name)
				break
			}
		}
	}
	if isServerFile(path) {
		s.Path = path
		for _, bin := range []string{
			filepath.Join(filepath.Dir(path), "scrcpy"),
			filepath.Join(filepath.Dir(path), "scrcpy.exe"),
			filepath.Join(filepath.Dir(path), "bin", "scrcpy"),
		} {
			if fileExists(bin) {
				s.Binary = bin
				break
			}
		}
	} else {
		s.Binary = path
		for _, cand := range serverCandidates(path) {
			if fileExists(cand) {
				s.Path = cand
				break
			}
		}
		if s.Path == "" {
			return s, errors.New("scrcpy-server was not found next to " + path)
		}
	}
	s.Version = readVersionFile(s.Path)
	if s.Version == "" {
		if m := serverFileVersionRe.FindStringSubmatch(filepath.Base(s.Path)); m != nil {
			s.Version = m[1]
		}
	}
	if s.Version == "" && s.Binary != "" {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, _ := proc.Command(ctx, s.Binary, "--version").CombinedOutput()
		s.Version = ParseScrcpyVersion(string(out))
	}
	return s, nil
}

func isServerFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasPrefix(base, "scrcpy-server") || strings.HasSuffix(base, ".jar")
}

func serverCandidates(bin string) []string {
	dir := filepath.Dir(bin)
	cands := []string{filepath.Join(dir, "scrcpy-server")}
	if env := os.Getenv("SCRCPY_SERVER_PATH"); env != "" {
		cands = append([]string{env}, cands...)
	}
	return append(cands,
		filepath.Join(dir, "..", "share", "scrcpy", "scrcpy-server"),
		filepath.Join(dir, "..", "Resources", "scrcpy-server"),
		"/opt/homebrew/share/scrcpy/scrcpy-server",
		"/usr/local/share/scrcpy/scrcpy-server",
	)
}

// readVersionFile reads the server's version from <server>.version, or from
// origin.json beside it, which the bundling scripts write.
func readVersionFile(server string) string {
	if data, err := os.ReadFile(server + ".version"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(string(data)), "v")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(server), "origin.json"))
	if err != nil {
		return ""
	}
	var origin struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(data, &origin) != nil {
		return ""
	}
	return strings.TrimPrefix(origin.Version, "v")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

var mismatchRe = regexp.MustCompile(`server version \(([^)]+)\) does not match the client`)

// ServerVersionFromMismatch reads the server's version out of the error it
// prints when the client announced another.
func ServerVersionFromMismatch(log string) string {
	if m := mismatchRe.FindStringSubmatch(log); m != nil {
		return m[1]
	}
	return ""
}
