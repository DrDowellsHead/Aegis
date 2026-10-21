// Package secrets reads server secrets from environment or private files.
package secrets

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

const maxSecretSize = 16 << 10

// Resolve reads NAME or NAME_FILE (never both). Files must be regular,
// non-symlinked and private on Unix. Secret contents never occur in errors.
func Resolve(name string) (string, error) {
	direct, directSet := os.LookupEnv(name)
	path, fileSet := os.LookupEnv(name + "_FILE")
	if (directSet && fileSet) || (fileSet && path == "") {
		return "", fmt.Errorf("%s and %s_FILE are mutually exclusive", name, name)
	}
	if !fileSet { return direct, nil }
	info, err := os.Lstat(path)
	if err != nil { return "", fmt.Errorf("%s_FILE cannot be read", name) }
	if !info.Mode().IsRegular() || info.Size() > maxSecretSize ||
		runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("%s_FILE must be a private regular file (0600 on Unix)", name)
	}
	f, err := os.Open(path)
	if err != nil { return "", fmt.Errorf("%s_FILE cannot be opened", name) }
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSecretSize+1))
	if err != nil || len(data) > maxSecretSize {
		return "", fmt.Errorf("%s_FILE cannot be read (maximum 16 KiB)", name)
	}
	value := strings.TrimSuffix(string(data), "\n")
	value = strings.TrimSuffix(value, "\r")
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("%s_FILE contains an empty or invalid secret", name)
	}
	return value, nil
}
