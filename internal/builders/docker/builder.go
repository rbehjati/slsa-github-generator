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

import intoto "github.com/in-toto/in-toto-golang/in_toto"


type DockerBuild struct {
	BuildDefinition *BuildDefinition
	BuildConfig BuildConfig
}
  
func NewDockerBuild(config *DockerBuildConfig) (*DockerBuild, error) {
	// TODO: Takes the valid DockerBuildConfig and assembles the BuildDefinition
	return nil, nil
}
  
func (d *DockerBuild) BuildArtifact() ([]intoto.Subject, error) {
	// TODO: Perform the build & return the digest of the binary
	return nil, nil
}

func DryRun(config *DockerBuildConfig) *BuildDefinition {
	artifacts := make(map[string]ArtifactReference)
	artifacts["source"] = config.GetSourceArtifact()
	artifacts["builderImage"] = config.GetBuilderImage()

	ep := ParameterCollection {
		Artifacts: artifacts,
		Values: map[string]string{"configFile": config.BuildConfigPath},
	}

	// Currently we don't have any SystemParameters or ResolvedDependencies. So these fields are left empty.
	return &BuildDefinition  {
		BuildType: DockerBasedBuildType,
		ExternalParameters: ep,
	}
}
