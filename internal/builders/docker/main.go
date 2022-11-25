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

package main

import (
	"flag"
	"log"
	"strings"
)

func main() {
	buildConfigPath := flag.String("build_config_path", "",
		"Required - Path to a toml file containing the build configs.")
	sourceRepo := flag.String("source_repo", "",
		"Required - URL of the source repo.")
	gitCommitHash := flag.String("git_commit_hash", "",
		"Required - SHA1 Git commit digest of the revision of the source code to build the artefact from.")
	builderImage := flag.String("builder_image", "",
		"Required - URL indicating the Docker builder image, including a URI and image digest.")

	flag.Parse()

	_, err := NewDockerBuildConfig(*sourceRepo, *gitCommitHash, *builderImage, *buildConfigPath)
	if err != nil {
		log.Fatalf("Could not build DockerBuildConfig: %v", err)
	}

	// TODO: Refactor and move to a builder function
	artifacts := make(map[string]ArtifactReference)
	artifacts["source"] = ArtifactReference {
		URI: *sourceRepo,
		Digest: toDigestMap(*gitCommitHash),
	}

	imageParts := strings.Split(*builderImage, "@")
	if len(imageParts) != 2 {
		log.Fatalf("got %s, want NAME@DIGEST format", *builderImage)
	}

	// TODO: check that imageParts[0] is a valid URI
	artifacts["builderImage"] = ArtifactReference {
		URI: imageParts[0],
		Digest: toDigestMap(imageParts[1]),
	}

	ep := ParameterCollection {
		Artifacts: artifacts,
		Values: map[string]string{"configFile": *buildConfigPath},
	}

	bd := BuildDefinition  {
		BuildType: DockerBasedBuildType,
		ExternalParameters: ep,
	}

	log.Printf("Test output: %v", bd)
}

func toDigestMap(input string) map[string]string {
	// We expect the input to be of the form ALG:VALUE
	parts := strings.Split(input, ":")
	if len(parts) != 2 {
		log.Fatalf("got %s, want ALG:VALUE format", input)
	}
	return map[string]string{parts[0]: parts[1]}

}