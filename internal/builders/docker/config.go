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

// DockerBuildConfig is a convenience class for collecting user inputs. 
type DockerBuildConfig struct {
	SourceRepo string
	SourceDigest string
	BuilderImage string
	BuildConfigPath string
}
  
// NewDockerBuildConfig validates the inputs and generates an instance of DockerBuildConfig.
func NewDockerBuildConfig(sourceRepo, sourceDigest, builderImage, buildConfigPath string) (*DockerBuildConfig, error) {
	if err := validateInputs(sourceRepo, sourceDigest, builderImage, buildConfigPath); err != nil {
		return nil, err
	}

	return &DockerBuildConfig {
		SourceRepo: sourceRepo,
		SourceDigest: sourceDigest,
		BuilderImage: builderImage,
		BuildConfigPath: buildConfigPath,
	}, nil
}

func validateInputs(sourceRepo, sourceDigest, builderImage, buildConfigPath string) error {
	// TODO(#1191): Validate the inputs
	// 1. sourceRepo is a valid URI
	// 2. gitCommitHash is valid (we expect to see sha1: at the beginning of the input string)
	// 3. Docker image is fully specified name@alg:digest
	// 4. buildConfigPath is relative
	return nil
}
