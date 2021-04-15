package configure

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/loft-sh/devspace/pkg/devspace/config/versions/latest"
	v1 "github.com/loft-sh/devspace/pkg/devspace/config/versions/latest"
	dockerfileutil "github.com/loft-sh/devspace/pkg/util/dockerfile"
	"github.com/loft-sh/devspace/pkg/util/ptr"
	"github.com/loft-sh/devspace/pkg/util/survey"
	"github.com/loft-sh/devspace/pkg/util/yamlutil"
	"github.com/pkg/errors"
)

var imageNameCleaningRegex = regexp.MustCompile("[^a-z0-9]")

// NewDockerfileComponentDeployment returns a new deployment that deploys an image built from a local dockerfile via a component
func (m *manager) NewDockerfileComponentDeployment(name, imageName, dockerfile, context string) (*latest.ImageConfig, *latest.DeploymentConfig, error) {
	var imageConfig *latest.ImageConfig
	var err error
	if imageName == "" {
		imageName = imageNameCleaningRegex.ReplaceAllString(strings.ToLower(name), "")
		imageConfig, err = m.newImageConfigFromDockerfile(imageName, dockerfile, context)
		if err != nil {
			return nil, nil, errors.Wrap(err, "get image config")
		}
		imageName = imageConfig.Image
	} else {
		imageConfig = m.newImageConfigFromImageName(imageName, dockerfile, context)
	}

	componentConfig := &latest.ComponentConfig{
		Containers: []*latest.ContainerConfig{
			{
				Image: imageName,
			},
		},
	}

	// Try to get ports from dockerfile
	port := ""
	ports, err := dockerfileutil.GetPorts(dockerfile)
	if err == nil {
		if len(ports) == 1 {
			port = strconv.Itoa(ports[0])
		} else if len(ports) > 1 {
			port, err = m.log.Question(&survey.QuestionOptions{
				Question:     "Which port is your application listening on?",
				DefaultValue: strconv.Itoa(ports[0]),
			})
			if err != nil {
				return nil, nil, err
			}

			if port == "" {
				port = strconv.Itoa(ports[0])
			}
		}
	}
	if port == "" {
		port, err = m.log.Question(&survey.QuestionOptions{
			Question: "Which port is your application listening on? (Enter to skip)",
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if port != "" {
		port, err := strconv.Atoi(port)
		if err != nil {
			return nil, nil, errors.Wrap(err, "parsing port")
		}

		componentConfig.Service = &latest.ServiceConfig{
			Ports: []*latest.ServicePortConfig{
				{
					Port: &port,
				},
			},
		}
	}

	retDeploymentConfig, err := generateComponentDeployment(name, componentConfig)
	if err != nil {
		return nil, nil, err
	}

	return imageConfig, retDeploymentConfig, nil
}

func generateComponentDeployment(name string, componentConfig *latest.ComponentConfig) (*latest.DeploymentConfig, error) {
	chartValues, err := yamlutil.ToInterfaceMap(componentConfig)
	if err != nil {
		return nil, err
	}

	// Prepare return deployment config
	retDeploymentConfig := &latest.DeploymentConfig{
		Name: name,
		Helm: &latest.HelmConfig{
			ComponentChart: ptr.Bool(true),
			Values:         chartValues,
		},
	}
	return retDeploymentConfig, nil
}

// NewKubectlDeployment retruns a new kubectl deployment
func (m *manager) NewKubectlDeployment(name, manifests string) (*latest.DeploymentConfig, error) {
	splitted := strings.Split(manifests, ",")
	splittedPointer := []string{}

	for _, s := range splitted {
		trimmed := strings.TrimSpace(s)
		splittedPointer = append(splittedPointer, trimmed)
	}

	return &v1.DeploymentConfig{
		Name: name,
		Kubectl: &v1.KubectlConfig{
			Manifests: splittedPointer,
		},
	}, nil
}

// NewHelmDeployment returns a new helm deployment
func (m *manager) NewHelmDeployment(name, chartName, chartRepo, chartVersion string) (*latest.DeploymentConfig, error) {
	retDeploymentConfig := &v1.DeploymentConfig{
		Name: name,
		Helm: &v1.HelmConfig{
			Chart: &v1.ChartConfig{
				Name: chartName,
			},
			Values: map[interface{}]interface{}{
				"someChartValue": "Add values for your chart here via `values` or `valuesFiles`",
			},
		},
	}

	if chartRepo != "" {
		retDeploymentConfig.Helm.Chart.RepoURL = chartRepo
	}
	if chartVersion != "" {
		retDeploymentConfig.Helm.Chart.Version = chartVersion
	}

	return retDeploymentConfig, nil
}
