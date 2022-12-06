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
	"fmt"
	"path/filepath"
	"net/url"
	"strings"
)

// Digest specifies a digest values, including the name of the hash function that was used for computing the digest. 
type Digest struct {
	Alg string
	Value string
}

// DockerImage fully specifies a docker image by a URI (e.g., including the docker image name and registry), and its digest.
type DockerImage struct {
	URI string
	Digest Digest
}

// DockerBuildConfig is a convenience class for holding validated user inputs. 
type DockerBuildConfig struct {
	SourceRepo string
	SourceDigest Digest
	BuilderImage DockerImage
	BuildConfigPath string
}
  
// NewDockerBuildConfig validates the inputs and generates an instance of DockerBuildConfig.
func NewDockerBuildConfig(sourceRepo, sourceDigest, builderImage, buildConfigPath string) (*DockerBuildConfig, error) {
	if err := validateURI(sourceRepo); err != nil {
		return nil, err
	}
	
	sourceRepoDigest, err := validateDigest(sourceDigest)
	if err != nil {
		return nil, err
	}

	dockerImage, err := validateDockerImage(builderImage)
	if err != nil {
		return nil, err
	}

	if filepath.IsAbs(buildConfigPath) {
		return nil, fmt.Errorf("build config path (%q) is not relative", buildConfigPath)
	}

	return &DockerBuildConfig {
		SourceRepo: sourceRepo,
		SourceDigest: *sourceRepoDigest,
		BuilderImage: *dockerImage,
		BuildConfigPath: buildConfigPath,
	}, nil
}

func validateURI(input string) error {
	_, err := url.Parse(input)
	if err != nil {
		return fmt.Errorf("could not parse string (%q) as URI: %v", input, err)
	}
	return nil
}

func validateDigest(input string) (*Digest, error) {
	// We expect the input to be of the form ALG:VALUE
	parts := strings.Split(input, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("got %s, want ALG:VALUE format", input)
	}
	digest := Digest {
		Alg: parts[0],
		Value: parts[1],
	}
	return &digest, nil
} 

func validateDockerImage(image string) (*DockerImage, error) {
	imageParts := strings.Split(image, "@")
	if len(imageParts) != 2 {
		return nil, fmt.Errorf("got %s, want NAME@DIGEST format", image)
	}

	if err := validateURI(imageParts[0]); err != nil {
		return nil, fmt.Errorf("docker image name (%q) is not a valid URI: %v", imageParts[0], err)
	} 

	digest, err := validateDigest(imageParts[1])
	if err != nil {
		return nil, fmt.Errorf("docker image digest (%q) is malformed: %v", imageParts[1], err)
	}

	dockerImage := DockerImage {
		URI: imageParts[0],
		Digest: *digest,
	}

	return &dockerImage, nil
}

// GetSourceArtifact returns the source repo and its digest as an instance of ArtifactReference.
func (config *DockerBuildConfig) GetSourceArtifact() ArtifactReference {
	return ArtifactReference {
		URI: config.SourceRepo,
		Digest: config.SourceDigest.toMap(),
	}
}

// GetBuilderImage returns the builder image as an instance of ArtifactReference.
func (config *DockerBuildConfig) GetBuilderImage() ArtifactReference {
	return ArtifactReference {
		URI: config.BuilderImage.URI,
		Digest: config.BuilderImage.Digest.toMap(),
	} 
}

func (d* Digest) toMap() map[string]string {
	return map[string]string{d.Alg: d.Value}
}
