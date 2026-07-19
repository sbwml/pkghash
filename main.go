package main

import (
	"crypto/sha256"
	"embed"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

//go:embed zstd
var embeddedZstd embed.FS

var (
	verbose bool
	save    bool
)

func main() {
	if u, err := user.Current(); err == nil && u.Uid == "0" {
		exe, _ := os.Executable()
		cmd := exec.Command("sudo", append([]string{"-u", "nobody", exe}, os.Args[1:]...)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		err := cmd.Run()
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		} else if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	flag.BoolVar(&verbose, "V", false, "Enable verbose output")
	flag.BoolVar(&save, "s", false, "Save the compressed tarball to current directory")
	flag.Parse()

	args := flag.Args()
	if len(args) != 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s [-V] [-s] <git_url> <commit_hash> <pkg_name> <pkg_version>\n", os.Args[0])
		os.Exit(1)
	}

	if err := run(args[0], args[1], args[2], args[3]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(gitURL, commit, pkgName, pkgVersion string) error {
	if err := checkDependencies(); err != nil {
		return err
	}

	zstdBin, cleanup, err := extractZstd()
	if err != nil {
		return err
	}
	defer cleanup()

	workDir, err := os.MkdirTemp("", "git-archive-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	subdir := fmt.Sprintf("%s-%s", pkgName, pkgVersion)
	tarGit := filepath.Join(workDir, subdir+".tar.git")
	outTarZst := filepath.Join(workDir, subdir+".tar.zst")
	cloneDir := filepath.Join(workDir, subdir)

	info("Cloning repository...")
	if err := runCmd("git", "clone", "-q", gitURL, cloneDir); err != nil {
		return err
	}

	if err := runCmdDir(cloneDir, "git", "checkout", "-q", commit); err != nil {
		return err
	}

	ts, err := getTimestamp(cloneDir)
	if err != nil {
		return err
	}
	info(fmt.Sprintf("Commit timestamp: @%d", ts))

	info("Generating formal git archive...")
	runCmdDir(cloneDir, "git", "config", "core.abbrev", "8")
	if err := runCmdDir(cloneDir, "git", "archive", "--format=tar", "HEAD", "--output="+tarGit); err != nil {
		return err
	}

	runCmdDir(cloneDir, "tar", "--numeric-owner", "--owner=0", "--group=0",
		"--ignore-failed-read", "-r", "-f", tarGit, ".git", ".gitmodules")

	extractDir := filepath.Join(workDir, "extract")
	os.Mkdir(extractDir, 0755)
	
	subDir := filepath.Join(extractDir, subdir)
	os.Mkdir(subDir, 0755)

	if err := runCmd("tar", "-C", subDir, "-xf", tarGit); err != nil {
		return err
	}

	info("Updating submodules...")
	runCmdDir(subDir, "git", "submodule", "update", "-q", "--init", "--recursive", "--")

	os.RemoveAll(filepath.Join(subDir, ".git"))
	os.Remove(filepath.Join(subDir, ".gitmodules"))

	info("Packing deterministic tarball...")
	if err := pack(subDir, outTarZst, time.Unix(ts, 0), zstdBin); err != nil {
		return err
	}

	hash, err := fileSHA256(outTarZst)
	if err != nil {
		return err
	}
	fmt.Println(hash)

	if save {
		copyFile(outTarZst, subdir+".tar.zst")
	}

	return nil
}

func checkDependencies() error {
	for _, dep := range []string{"git", "tar"} {
		if _, err := exec.LookPath(dep); err != nil {
			return fmt.Errorf("missing %s", dep)
		}
	}
	return nil
}

func extractZstd() (string, func(), error) {
	data, err := embeddedZstd.ReadFile("zstd")
	if err != nil {
		return "", nil, err
	}

	f, err := os.CreateTemp("", "zstd-*")
	if err != nil {
		return "", nil, err
	}
	p := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(p)
		return "", nil, err
	}
	f.Close()

	if err := os.Chmod(p, 0755); err != nil {
		os.Remove(p)
		return "", nil, err
	}
	return p, func() { os.Remove(p) }, nil
}

func info(msg string) {
	if verbose {
		fmt.Fprintf(os.Stderr, "=> %s\n", msg)
	}
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if verbose {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	return cmd.Run()
}

func runCmdDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if verbose {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	return cmd.Run()
}

func getTimestamp(dir string) (int64, error) {
	cmd := exec.Command("git", "log", "-1", "--no-show-signature", "--format=@%ct")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	var ts int64
	if _, err = fmt.Sscanf(strings.TrimSpace(string(out)), "@%d", &ts); err != nil {
		return 0, err
	}
	return ts, nil
}

func pack(src, dst string, mtime time.Time, zstdBin string) error {
	tar := exec.Command("tar", "--numeric-owner", "--owner=0", "--group=0", "--mode=a-s", "--sort=name",
		fmt.Sprintf("--mtime=%s", mtime.UTC().Format("2006-01-02T15:04:05Z")), "-c", filepath.Base(src))
	tar.Dir = filepath.Dir(src)

	tarOut, err := tar.StdoutPipe()
	if err != nil {
		return err
	}

	if err := tar.Start(); err != nil {
		return err
	}

	zcmd := exec.Command(zstdBin, "-q", "-T0", "--ultra", "-20", "-c")
	zcmd.Stdin = tarOut
	zcmd.Stderr = os.Stderr

	f, err := os.Create(dst)
	if err != nil {
		tar.Wait()
		return err
	}
	defer f.Close()

	zcmd.Stdout = f

	if err := zcmd.Start(); err != nil {
		tar.Wait()
		return err
	}

	tar.Wait()
	zcmd.Wait()
	return nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	io.Copy(h, f)
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer df.Close()

	io.Copy(df, sf)
	return nil
}
