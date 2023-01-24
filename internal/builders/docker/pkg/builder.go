// Copyright 2022 SLSA Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package pkg

// This file contains the structs and functionality for building artifacts
// using a builder Docker image, and user-provided build configurations.
//
// In particular, this file defines a GitClient struct for handling git
// commands for fetching the repo at a Git commit hash. It also defines an
// exposed Builder struct for handling the steps of building artifacts using a
// Docker image.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	intoto "github.com/in-toto/in-toto-golang/in_toto"

	"github.com/slsa-framework/slsa-github-generator/internal/errors"
)

// errGitCommitMismatch indicates that the repo is checked out at an unexpected commit hash.
type errGitCommitMismatch struct {
	errors.WrappableError
}

// errGitFetch indicates an error when cloning a Git repo.
type errGitFetch struct {
	errors.WrappableError
}

// errGitCheckout indicates an error when checking out a given commit hash.
type errGitCheckout struct {
	errors.WrappableError
}

// DockerBuild represents a state in the process of building the artifacts
// where the source repository is checked out and the config file is loaded and
// parsed, and we are ready for running the `docker run` command.
type DockerBuild struct {
	config      *DockerBuildConfig
	buildConfig *BuildConfig
	RepoInfo    *RepoCheckoutInfo
}

// RepoCheckoutInfo contains info about the location of a locally checked out
// repository.
type RepoCheckoutInfo struct {
	// Path to the root of the repo.
	RepoRoot string
}

// Fetcher is an interface with a single method Fetch, for fetching a
// repository from its source.
type Fetcher interface {
	Fetch() (*RepoCheckoutInfo, error)
	EffectiveDigest() Digest
}

// Builder is responsible for setting up the environment and using docker
// commands to build artifacts as specified in a DockerBuildConfig.
type Builder struct {
	repoFetcher Fetcher
	config      DockerBuildConfig
}

// NewBuilderWithGitFetcher creates a new Builder that fetches the sources
// from a Git repository.
func NewBuilderWithGitFetcher(config DockerBuildConfig) (*Builder, error) {
	gc, err := newGitClient(&config, 0 /* depth */)
	if err != nil {
		return nil, fmt.Errorf("could not create builder: %v", err)
	}

	return &Builder{
		repoFetcher: gc,
		config:      config,
	}, nil
}

// CreateBuildDefinition creates a BuildDefinition from the DockerBuildConfig
// and BuildConfig in this DockerBuild.
func (db *DockerBuild) CreateBuildDefinition() *BuildDefinition {
	artifacts := make(map[string]ArtifactReference)
	artifacts[SourceKey] = sourceArtifact(db.config)
	artifacts[BuilderImageKey] = builderImage(db.config)

	// The input is a simple string array, no errors occur. But we check and
	// print the error to make the linters happy.
	cmdBytes, err := json.Marshal(db.buildConfig.Command)
	if err != nil {
		log.Printf("Could not unmarshal command array: %v.", err)
	}

	cmd := string(cmdBytes)

	ep := ParameterCollection{
		Artifacts: artifacts,
		Values: map[string]string{
			ConfigFileKey:   db.config.BuildConfigPath,
			ArtifactPathKey: db.buildConfig.ArtifactPath,
			CommandKey:      cmd,
		},
	}

	// Currently we don't have any SystemParameters or ResolvedDependencies.
	// So these fields are left empty.
	return &BuildDefinition{
		BuildType:          DockerBasedBuildType,
		ExternalParameters: ep,
	}
}

// sourceArtifact returns the source repo and its digest as an instance of ArtifactReference.
func sourceArtifact(config *DockerBuildConfig) ArtifactReference {
	return ArtifactReference{
		URI:    config.SourceRepo,
		Digest: config.SourceDigest.ToMap(),
	}
}

// builderImage returns the builder image as an instance of ArtifactReference.
func builderImage(config *DockerBuildConfig) ArtifactReference {
	return ArtifactReference{
		URI:    config.BuilderImage.ToString(),
		Digest: config.BuilderImage.Digest.ToMap(),
	}
}

// SetUpBuildState sets up the build by checking out the source repository and
// loading the config file. It returns an instance of DockerBuild, or an error
// if setting up the build state fails.
func (b *Builder) SetUpBuildState() (*DockerBuild, error) {
	// 1. Check out the repo, or verify that it is checked out.
	repoInfo, err := b.repoFetcher.Fetch()
	if err != nil {
		return nil, fmt.Errorf("couldn't verify or fetch source repo: %v", err)
	}

	// 2. Load and parse the config file.
	bc, err := b.config.LoadBuildConfigFromFile()
	if err != nil {
		return nil, fmt.Errorf("couldn't load config file from %q: %v", b.config.BuildConfigPath, err)
	}

	// 3. Check that the ArtifactPath pattern does not match any existing files,
	// so that we don't accidentally generate provenances for the wrong files.
	if err := checkExistingFiles(bc.ArtifactPath); err != nil {
		return nil, err
	}

	// Make a copy of b.config, but update its SourceDigest to the effective
	// commit digest from the fetcher.
	c := &DockerBuildConfig{
		SourceRepo:         b.config.SourceRepo,
		SourceDigest:       b.repoFetcher.EffectiveDigest(),
		BuilderImage:       b.config.BuilderImage,
		BuildConfigPath:    b.config.BuildConfigPath,
		ResolutionStrategy: b.config.ResolutionStrategy,
	}

	db := &DockerBuild{
		config:      c,
		buildConfig: bc,
		RepoInfo:    repoInfo,
	}
	return db, nil
}

// BuildArtifacts builds the artifacts based on the user-provided inputs, and
// returns the names and SHA256 digests of the generated artifacts.
func (db *DockerBuild) BuildArtifacts() ([]intoto.Subject, error) {
	if err := runDockerRun(db); err != nil {
		return nil, fmt.Errorf("running `docker run` failed: %v", err)
	}
	return inspectArtifacts(db.buildConfig.ArtifactPath)
}

func runDockerRun(db *DockerBuild) error {
	// Get the current working directory. We will mount it as a Docker volume.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("couldn't get the current working directory: %v", err)
	}

	defaultDockerRunFlags := []string{
		// Mount the current working directory to workspace.
		fmt.Sprintf("--volume=%s:/workspace", cwd),
		"--workdir=/workspace",
		// Remove the container file system after the container exits.
		"--rm",
	}

	buildDef := db.CreateBuildDefinition()

	var args []string
	args = append(args, "run")
	args = append(args, defaultDockerRunFlags...)
	args = append(args, buildDef.ExternalParameters.Artifacts[BuilderImageKey].URI)
	args = append(args, db.buildConfig.Command...)
	cmd := exec.Command("docker", args...)

	log.Printf("Running command: %q.", cmd.String())

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("couldn't get the command's stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("couldn't get the command's stderr: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("couldn't start the 'git checkout' command: %v", err)
	}

	files, err := saveToTempFile(stdout, stderr)
	if err != nil {
		return fmt.Errorf("cannot save logs and errs to file: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("failed to complete the command: %v; see %s for logs, and %s for errors",
			err, files[0], files[1])
	}

	return nil
}

// GitClient provides data and functions for fetching the source files from a
// Git repository.
type GitClient struct {
	sourceRepo         *string
	sourceDigest       *Digest
	effectiveDigest    *Digest
	checkoutInfo       *RepoCheckoutInfo
	logFiles           []string
	errFiles           []string
	resolutionStrategy int
	depth              int
}

func newGitClient(config *DockerBuildConfig, depth int) (*GitClient, error) {
	repo := config.SourceRepo
	parsed, err := url.Parse(repo)
	if err != nil {
		return nil, fmt.Errorf("could not parse repo URI: %v", err)
	}

	switch parsed.Scheme {
	case "https":
		break
	case "git+https":
		repo = strings.Replace(repo, "git+https", "https", 1)
	case "https+git":
		repo = strings.Replace(repo, "https+git", "https", 1)
	default:
		return nil, fmt.Errorf("unsupported scheme: %v", parsed.Scheme)
	}

	return &GitClient{
		sourceRepo:         &repo,
		sourceDigest:       &config.SourceDigest,
		effectiveDigest:    &config.SourceDigest,
		resolutionStrategy: config.ResolutionStrategy,
		depth:              depth,
		checkoutInfo:       &RepoCheckoutInfo{},
	}, nil
}

func (c *GitClient) cleanupAllFiles() {
	c.checkoutInfo.Cleanup()
	for _, file := range append(c.logFiles, c.errFiles...) {
		if err := os.Remove(file); err != nil {
			log.Printf("failed to remove temp file %q: %v", file, err)
		}
	}
}

// Fetch is implemented for GitClient to make it usable in contexts where a
// Fetcher is needed.
func (c *GitClient) Fetch() (*RepoCheckoutInfo, error) {
	if err := c.verifyOrFetchRepo(); err != nil {
		return nil, err
	}
	return c.checkoutInfo, nil
}

// EffectiveDigest returns the effective digest in this GitClient.
func (c *GitClient) EffectiveDigest() Digest {
	return Digest{
		Alg:   c.effectiveDigest.Alg,
		Value: c.effectiveDigest.Value,
	}
}

// verifyOrFetchRepo checks that the current working directly is a Git
// repository at the expected Git commit hash. If the repo is checked out at a
// different commit, then based on the GitClient's resolution strategy one of
// the following is done:
//   - 'abort': terminates with an error,
//   - 'checkout': checks out the repo at the given commit.
//   - 'ignore': updates the effective commit to the checked out commit.
func (c *GitClient) verifyOrFetchRepo() error {
	if c.sourceDigest.Alg != "sha1" {
		return fmt.Errorf("git commit digest must be a sha1 digest")
	}
	needsCheckout, err := c.needsCheckout()
	if err != nil {
		return err
	}
	if needsCheckout {
		if err := c.fetchSourcesFromGitRepo(); err != nil {
			return fmt.Errorf("couldn't fetch sources from %q at commit %q: %v", *c.sourceRepo, c.sourceDigest, err)
		}
	}
	return nil
}

// needsCheckout checks if the source code needs to be fetched from the Git
// repository. Returns true in the following cases:
// * If the current directory is not the root of a Git repository,
// * The commit hashes do not match, and the resolution strategy is 'checkout'.
// Returns false, and no error if:
//   - The repository is checked out at the given commit hash,
//   - The repository is checked out at a different commit hash, and the
//     resolution strategy is 'ignore'. In this case, the effectiveDigest in
//     GitClient is updated to contain the actual checked-out commit digest.
//
// Returns false, and an error if the commit hashes do not match, and the
// resolution strategy is 'abort'.
func (c *GitClient) needsCheckout() (bool, error) {
	cmd := exec.Command("git", "rev-parse", "--verify", "HEAD")
	lastCommitIDBytes, err := cmd.Output()
	if err != nil {
		// The current working directory is not a git repo.
		return true, nil
	}
	lastCommitID := strings.TrimSpace(string(lastCommitIDBytes))

	if lastCommitID != c.sourceDigest.Value {
		switch c.resolutionStrategy {
		case CheckoutToResolve:
			return true, nil
		case IgnoreToResolve:
			c.effectiveDigest = &Digest{
				Alg:   "sha1",
				Value: lastCommitID,
			}
			return false, nil
		case AbortToResolve:
			return false, errors.Errorf(&errGitCommitMismatch{},
				"the repo is already checked out at a different commit (%q)", lastCommitID)
		}
	}

	return false, nil
}

// fetchSourcesFromGitRepo clones a repo from the URL given in this GitClient,
// up to the depth given in this GitClient, into a temporary directory. It then
// checks out the specified commit. If depth is not a positive number, the
// entire repo and its history is cloned.
// Returns an error if the repo cannot be cloned, or the commit hash does not
// exist. Otherwise, updates this GitClient with RepoCheckoutInfo containing
// the absolute path of the root of the repo, and other generated files paths.
func (c *GitClient) fetchSourcesFromGitRepo() error {
	// create a temp folder in the current directory for fetching the repo.
	targetDir, err := os.MkdirTemp("", "release-*")
	if err != nil {
		return fmt.Errorf("couldn't create temp directory: %v", err)
	}
	log.Printf("Checking out the repo in %q.", targetDir)

	// Make targetDir and its parents, and cd to it.
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("couldn't create directories at %q: %v", targetDir, err)
	}
	if err := os.Chdir(targetDir); err != nil {
		return fmt.Errorf("couldn't change directory to %q: %v", targetDir, err)
	}

	// Clone the repo.
	if err = c.cloneGitRepo(); err != nil {
		return errors.Errorf(&errGitFetch{}, "couldn't clone the Git repo: %w", err)
	}

	// Change directory to the root of the cloned repo.
	repoName := path.Base(*c.sourceRepo)
	if err := os.Chdir(repoName); err != nil {
		return fmt.Errorf("couldn't change directory to %q: %v", repoName, err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("couldn't get current working directory: %v", err)
	}

	// Checkout the commit.
	if err = c.checkoutGitCommit(); err != nil {
		return errors.Errorf(&errGitCheckout{}, "couldn't checkout the Git commit: %w", err)
	}

	c.checkoutInfo.RepoRoot = cwd

	return nil
}

// Clones a Git repo from the URI in this GitClient, up to the depth given in
// this GitClient. If depth is 0 or negative, the entire repo is cloned.
func (c *GitClient) cloneGitRepo() error {
	//#nosec G204 -- Input from user config file.
	cmd := exec.Command("git", "clone", *c.sourceRepo)
	if c.depth > 0 {
		//#nosec G204 -- Input from user config file.
		cmd = exec.Command("git", "clone", "--depth", fmt.Sprintf("%d", c.depth), *c.sourceRepo)
	}
	log.Printf("Cloning the repo from %s...", *c.sourceRepo)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("couldn't get the command's stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("couldn't get the command's stderr: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("couldn't start the 'git checkout' command: %v", err)
	}

	files, err := saveToTempFile(stdout, stderr)
	if err != nil {
		return fmt.Errorf("cannot save logs and errs to file: %v", err)
	}
	c.logFiles = append(c.logFiles, files[0])
	c.errFiles = append(c.errFiles, files[1])

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("failed to complete the command: %v; see %q for logs, and %q for errors",
			err, files[0], files[1])
	}
	log.Printf("'git clone' completed. See %q, and %q for logs, and errors.", files[0], files[1])

	return nil
}

func (c *GitClient) checkoutGitCommit() error {
	//#nosec G204 -- Input from user config file.
	cmd := exec.Command("git", "checkout", c.sourceDigest.Value)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("couldn't get the command's stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("couldn't get the command's stderr: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("couldn't start the 'git checkout' command: %v", err)
	}

	files, err := saveToTempFile(stdout, stderr)
	if err != nil {
		return fmt.Errorf("cannot save logs and errs to file: %v", err)
	}
	c.logFiles = append(c.logFiles, files[0])
	c.errFiles = append(c.errFiles, files[1])

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("failed to complete the command: %v; see %q for logs, and %q for errors",
			err, files[0], files[1])
	}

	return nil
}

// saveToTempFile creates a tempfile in `/tmp` and writes the content of the
// given reader to that file.
func saveToTempFile(readers ...io.Reader) ([]string, error) {
	var files []string
	for _, reader := range readers {
		bytes, err := io.ReadAll(reader)
		if err != nil {
			return files, err
		}

		tmpfile, err := os.CreateTemp("", "log-*.txt")
		if err != nil {
			return files, fmt.Errorf("couldn't create tempfile: %v", err)
		}

		if _, err := tmpfile.Write(bytes); err != nil {
			tmpfile.Close()
			return files, fmt.Errorf("couldn't write bytes to tempfile: %v", err)
		}
		files = append(files, tmpfile.Name())
	}

	return files, nil
}

// Checks if any files match the given pattern, and returns an error if so.
func checkExistingFiles(pattern string) error {
	matches, err := filepath.Glob(pattern)
	// The only possible error is ErrBadPattern.
	if err != nil {
		return fmt.Errorf("the pattern (%q) is malformed: %v", pattern, err)
	}

	if len(matches) == 0 {
		return nil
	}
	return fmt.Errorf("the specified pattern (%q) matches %d existing files; expected no matches", pattern, len(matches))
}

// Finds all files matching the given pattern, measures the SHA256 digest of
// each file, and returns filenames and digests as an array of intoto.Subject.
// Precondition: The pattern is a relative file path pattern.
func inspectArtifacts(pattern string) ([]intoto.Subject, error) {
	matches, err := filepath.Glob(pattern)
	// The only possible error is ErrBadPattern.
	if err != nil {
		return nil, fmt.Errorf("the pattern (%q) is malformed: %v", pattern, err)
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("no files matching the pattern %q: %v", pattern, err)
	}

	var subjects []intoto.Subject
	for _, path := range matches {
		subject, err := toIntotoSubject(path)
		if err != nil {
			return nil, err
		}
		subjects = append(subjects, *subject)
	}

	return subjects, nil
}

// Reads the file in the given path and returns its name and digest wrapped in
// an intoto.Subject.
func toIntotoSubject(path string) (*intoto.Subject, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("couldn't read file %q: %v", path, err)
	}

	sum256 := sha256.Sum256(data)
	digest := hex.EncodeToString(sum256[:])
	name := filepath.Base(path)
	subject := &intoto.Subject{
		Name:   name,
		Digest: map[string]string{"sha256": digest},
	}
	return subject, nil
}

// Cleanup removes the generated temp files. But it might not be able to remove
// all the files, for instance the ones generated by the build script.
func (info *RepoCheckoutInfo) Cleanup() {
	// Some files are generated by the build toolchain (e.g., cargo), and cannot
	// be removed. We still want to remove all other files to avoid taking up
	// too much space, particularly when running locally.
	if len(info.RepoRoot) == 0 {
		return
	}
	if err := os.RemoveAll(info.RepoRoot); err != nil {
		log.Printf("failed to remove the temp files: %v", err)
	}
}
