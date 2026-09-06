package ledger_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerVerifiesDownloadAndConnects(t *testing.T) {
	installer, err := filepath.Abs("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, arch       string
		corrupt, missing bool
	}{
		{name: "amd64", arch: "x86_64"}, {name: "arm64", arch: "aarch64"},
		{name: "bad checksum", arch: "x86_64", corrupt: true}, {name: "no release", arch: "x86_64", missing: true},
		{name: "unsupported architecture", arch: "riscv64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mockBin := filepath.Join(root, "bin")
			assets := filepath.Join(root, "assets")
			destination := filepath.Join(root, "install with spaces")
			for _, dir := range []string{mockBin, assets, destination} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			binary := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LEDGER_TEST_ARGS\"\n"
			sum := sha256.Sum256([]byte(binary))
			for _, arch := range []string{"amd64", "arm64"} {
				write(filepath.Join(assets, "ledger_linux_"+arch), binary)
			}
			write(filepath.Join(assets, "SHA256SUMS"), fmt.Sprintf("%x  ledger_linux_amd64\n%x  ledger_linux_arm64\n", sum, sum))
			write(filepath.Join(mockBin, "uname"), "#!/bin/sh\nif [ \"$1\" = '-s' ]; then echo Linux; else echo \"$LEDGER_TEST_ARCH\"; fi\n")
			write(filepath.Join(mockBin, "curl"), `#!/bin/sh
if [ "$LEDGER_TEST_MISSING" = 1 ]; then exit 22; fi
while [ "$#" -gt 0 ]; do
 case "$1" in
  https://*) source=${1##*/} ;;
  -o) shift; destination=$1 ;;
 esac
 shift
done
if [ "$LEDGER_TEST_CORRUPT" = 1 ] && [ "$source" != SHA256SUMS ]; then
 printf corrupt > "$destination"
else
 cp "$LEDGER_TEST_ASSETS/$source" "$destination"
fi
`)
			installed := filepath.Join(destination, "ledger")
			write(installed, "existing installation")
			argsFile := filepath.Join(root, "args")
			t.Setenv("PATH", mockBin+":"+os.Getenv("PATH"))
			t.Setenv("LEDGER_INSTALL_DIR", destination)
			t.Setenv("LEDGER_TEST_ARCH", tc.arch)
			t.Setenv("LEDGER_TEST_ASSETS", assets)
			t.Setenv("LEDGER_TEST_ARGS", argsFile)
			t.Setenv("LEDGER_TEST_CORRUPT", map[bool]string{true: "1", false: "0"}[tc.corrupt])
			t.Setenv("LEDGER_TEST_MISSING", map[bool]string{true: "1", false: "0"}[tc.missing])
			result, err := exec.Command("sh", installer, "--server", "https://ledger.example.com", "--name", "My laptop").CombinedOutput()
			body, readErr := os.ReadFile(installed)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.corrupt || tc.missing || tc.arch == "riscv64" {
				if err == nil || string(body) != "existing installation" {
					t.Fatalf("failed install replaced binary: %v %s", err, result)
				}
				if _, err = os.Stat(argsFile); !os.IsNotExist(err) {
					t.Fatal("ran an unverified binary")
				}
			} else {
				if err != nil || string(body) != binary {
					t.Fatalf("install: %v %s", err, result)
				}
				args, err := os.ReadFile(argsFile)
				if err != nil || string(args) != "connect\ncodex\n--server\nhttps://ledger.example.com\n--name\nMy laptop\n" {
					t.Fatalf("connection args: %q %v", args, err)
				}
			}
			entries, err := os.ReadDir(destination)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".ledger.") {
					t.Fatal("staged binary left behind")
				}
			}
		})
	}
}
