// SPDX-License-Identifier: Apache-2.0

package gitinterface

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5"
	"github.com/jonboulle/clockwork"
)

const (
	binary           = "git"
	committerTimeKey = "GIT_COMMITTER_DATE"
	authorTimeKey    = "GIT_AUTHOR_DATE"
)

// Repository is a lightweight wrapper around a Git repository. It stores the
// location of the repository's GIT_DIR.
type Repository struct {
	gitDirPath string
	clock      clockwork.Clock
}

// GetGoGitRepository returns the go-git representation of a repository. We use
// this in certain signing and verifying workflows.
func (r *Repository) GetGoGitRepository() (*git.Repository, error) {
	return git.PlainOpenWithOptions(r.gitDirPath, &git.PlainOpenOptions{DetectDotGit: true})
}

// GetGitDir returns the GIT_DIR path for the repository.
func (r *Repository) GetGitDir() string {
	return r.gitDirPath
}

// LoadRepository returns a Repository instance using the current working
// directory. It also inspects the PATH to ensure Git is installed.
func LoadRepository() (*Repository, error) {
	_, err := exec.LookPath(binary)
	if err != nil {
		return nil, fmt.Errorf("unable to find Git binary, is Git installed?")
	}

	repo := &Repository{clock: clockwork.NewRealClock()}
	envVar := os.Getenv("GIT_DIR")
	if envVar != "" {
		repo.gitDirPath = envVar
		return repo, nil
	}

	stdOut, stdErr, err := repo.executor("rev-parse", "--git-dir").executeRaw()
	if err != nil {
		errContents, newErr := io.ReadAll(stdErr)
		if newErr != nil {
			return nil, fmt.Errorf("unable to read original err '%w' when loading repository: %w", err, newErr)
		}
		return nil, fmt.Errorf("unable to identify GIT_DIR: %w: %s", err, strings.TrimSpace(string(errContents)))
	}

	stdOutContents, err := io.ReadAll(stdOut)
	if err != nil {
		return nil, fmt.Errorf("unable to identify GIT_DIR: %w", err)
	}
	repo.gitDirPath = strings.TrimSpace(string(stdOutContents))

	return repo, nil
}

// executor is a lightweight wrapper around exec.Cmd to run Git commands. It
// accepts the arguments to the `git` binary, but the binary itself must not be
// specified.
type executor struct {
	r     *Repository
	args  []string
	env   []string
	stdIn *bytes.Buffer

	setGitDirOnce sync.Once
}

// executor initializes a new executor instance to run a Git command with the
// specified arguments.
func (r *Repository) executor(args ...string) *executor {
	return &executor{r: r, args: args, env: os.Environ()}
}

// withEnv adds the specified environment variables. Each environment variable
// must be specified in the form of `key=value`.
func (e *executor) withEnv(env ...string) *executor {
	e.env = append(e.env, env...)
	return e
}

// withGitDir adds the `--git-dir` parameter to the command. It executes only
// once.
func (e *executor) withGitDir() *executor {
	e.setGitDirOnce.Do(func() {
		e.args = append([]string{"--git-dir", e.r.gitDirPath}, e.args...)
	})
	return e
}

// withStdIn sets the contents of stdin to be passed in to the command.
func (e *executor) withStdIn(stdIn *bytes.Buffer) *executor {
	e.stdIn = stdIn
	return e
}

// execute runs the constructed Git command and returns the contents of stdout.
// Leading and trailing spaces and newlines are removed. This function also sets
// the Git directory. This function should be used almost every time; the only
// exception is when the Git directory must not be set and/or the output is
// desired without any processing such as the removal of space characters.
func (e *executor) execute() (string, error) {
	e.withGitDir()

	stdOut, stdErr, err := e.executeRaw()
	if err != nil {
		stdErrContents, newErr := io.ReadAll(stdErr)
		if newErr != nil {
			return "", fmt.Errorf("unable to read stderr contents: %w; original err: %w", newErr, err)
		}
		return "", fmt.Errorf("%w when executing `git %s`: %s", err, strings.Join(e.args, " "), string(stdErrContents))
	}

	stdOutContents, err := io.ReadAll(stdOut)
	if err != nil {
		return "", fmt.Errorf("unable to read stdout contents: %w", err)
	}

	return strings.TrimSpace(string(stdOutContents)), nil
}

// executeRaw runs the constructed Git command with no additional processing.
func (e *executor) executeRaw() (io.Reader, io.Reader, error) {
	cmd := exec.Command(binary, e.args...) //nolint:gosec
	cmd.Env = e.env

	var (
		stdOut bytes.Buffer
		stdErr bytes.Buffer
	)

	cmd.Stdout = &stdOut
	cmd.Stderr = &stdErr

	if e.stdIn != nil {
		cmd.Stdin = e.stdIn
	}

	return &stdOut, &stdErr, cmd.Run()
}
