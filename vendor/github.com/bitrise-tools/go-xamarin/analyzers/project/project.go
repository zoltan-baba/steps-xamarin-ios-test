package project

import (
	"bufio"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bitrise-io/go-utils/fileutil"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-tools/go-xamarin/constants"
	"github.com/bitrise-tools/go-xamarin/utility"
)

const (
	targetDefinitionPattern = `(?i)Import Project="(?P<target_definition>.*\.targets)"`

	typeGUIDsPattern    = `(?i)<ProjectTypeGuids>(?P<project_type_guids>.*)<\/ProjectTypeGuids>`
	guidPattern         = `(?i)<ProjectGuid>{(?P<project_id>.*)}<\/ProjectGuid>`
	outputTpyePattern   = `(?i)<OutputType>(?P<output_type>.*)<\/OutputType>`
	assemblyNamePattern = `(?i)<AssemblyName>(?P<assembly_name>.*)<\/AssemblyName>`

	// PropertyGroup with Condition
	propertyGroupStartPattern                                 = `(?i)<PropertyGroup>`
	propertyGroupWithConditionConfigurationAndPlatformPattern = `(?i)<PropertyGroup Condition="\s*'\$\(Configuration\)\|\$\(Platform\)'\s*==\s*'(?P<config>.*)\|(?P<platform>.*)'\s*">`
	propertyGroupWithConditionConfigurationPattern            = `(?i)<PropertyGroup Condition="\s*'\$\(Configuration\)'\s*==\s*'(?P<config>.*)'\s*">`
	propertyGroupWithConditionPlatformPattern                 = `(?i)<PropertyGroup Condition="\s*'\$\(Platform\)'\s*==\s*'(?P<platform>.*)'\s*">`
	propertyGroupEndPattern                                   = `(?i)</PropertyGroup>`

	outputPathPattern = `(?i)<OutputPath>(?P<output_path>.*)<\/OutputPath>`

	// ItemGroup
	projectRefernceStartPattern = `(?i)<ProjectReference Include="(?P<project_path>.*)">`
	projectRefernceEndPattern   = `(?i)</ProjectReference>`
	referredProjectIDPattern    = `(?i)<Project>{(?P<id>.*)}<\/Project>`

	// Xamarin.iOS specific
	ipaPackageNamePattern = `(?i)<IpaPackageName>`
	buildIpaPattern       = `(?i)<BuildIpa>True</BuildIpa>`
	mtouchArchPattern     = `(?i)<MtouchArch>(?P<arch>.*)<\/MtouchArch>`

	// Xamarin.Android specific
	manifestPattern           = `(?i)<AndroidManifest>(?P<manifest_path>.*)<\/AndroidManifest>`
	androidApplicationPattern = `(?i)<AndroidApplication>True<\/AndroidApplication>`
	androidKeystorePattern    = `(?i)<AndroidKeyStore>True<\/AndroidKeyStore>`

	// Testing frameworks
	referenceXamarinUITestPattern = `(?i)Include="Xamarin.UITest`
	referenceNunitFramework       = `(?i)Include="nunit.framework`
	referenceNunitLiteFramework   = `(?i)Include="MonoTouch.NUnitLite`
)

// ConfigurationPlatformModel ...
type ConfigurationPlatformModel struct {
	Configuration string
	Platform      string
	OutputDir     string

	MtouchArchs []string
	BuildIpa    bool

	SignAndroid bool
}

// Model ...
type Model struct {
	Pth  string
	Name string // Set by solution analyze or set its path's filename without extension

	// Solution Configuration|Platform - Project Configuration|Platform map
	// !!! only set by solution analyze
	ConfigMap map[string]string

	ID           string
	ProjectType  constants.ProjectType
	OutputType   string
	AssemblyName string

	TestFrameworks     []constants.TestFramework
	ReferredProjectIDs []string

	ManifestPth        string
	AndroidApplication bool

	Configs map[string]ConfigurationPlatformModel // Project Configuration|Platform - ConfigurationPlatformModel map
}

// New ...
func New(pth string) (Model, error) {
	return analyzeProject(pth)
}

func analyzeTargetDefinition(project Model, pth string) (Model, error) {
	configurationPlatform := ConfigurationPlatformModel{}

	isPropertyGroupSection := false
	isProjectReferenceSection := false

	projectDir := filepath.Dir(pth)

	projectDefinitionFileContent, err := fileutil.ReadStringFromFile(pth)
	if err != nil {
		return Model{}, fmt.Errorf("failed to read project (%s), error: %s", pth, err)
	}

	scanner := bufio.NewScanner(strings.NewReader(projectDefinitionFileContent))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Target definition
		// Analyze target definition and point the current project to the target analyze result
		if matches := regexp.MustCompile(targetDefinitionPattern).FindStringSubmatch(line); len(matches) == 2 {
			targetDefinitionRelativePth := utility.FixWindowsPath(matches[1])

			if !strings.Contains(targetDefinitionRelativePth, "$(MSBuild") {
				targetDefinitionPth := filepath.Join(projectDir, targetDefinitionRelativePth)

				if exist, err := pathutil.IsPathExists(targetDefinitionPth); err != nil {
					return Model{}, err
				} else if exist {
					projectFromTargetDefinition, err := analyzeTargetDefinition(project, targetDefinitionPth)
					if err != nil {
						return Model{}, err
					}

					// Set properties became from solution analyze
					projectFromTargetDefinition.Name = project.Name
					projectFromTargetDefinition.Pth = project.Pth
					projectFromTargetDefinition.ConfigMap = project.ConfigMap
					// ---

					project = projectFromTargetDefinition
				}
			}

			continue
		}

		// ProjectGuid
		if matches := regexp.MustCompile(guidPattern).FindStringSubmatch(line); len(matches) == 2 {
			project.ID = matches[1]
			continue
		}

		// OutputType
		if matches := regexp.MustCompile(outputTpyePattern).FindStringSubmatch(line); len(matches) == 2 {
			project.OutputType = strings.ToLower(matches[1])
			continue
		}

		// AssemblyName
		if matches := regexp.MustCompile(assemblyNamePattern).FindStringSubmatch(line); len(matches) == 2 {
			project.AssemblyName = matches[1]
			continue
		}

		// AndroidManifest
		if matches := regexp.MustCompile(manifestPattern).FindStringSubmatch(line); len(matches) == 2 {
			manifestRelativePth := utility.FixWindowsPath(matches[1])

			project.ManifestPth = filepath.Join(projectDir, manifestRelativePth)
			continue
		}

		// AndroidApplication
		if match := regexp.MustCompile(androidApplicationPattern).FindString(line); match != "" {
			project.AndroidApplication = true
			continue
		}

		//
		// PropertyGroups

		if isPropertyGroupSection {
			if match := regexp.MustCompile(propertyGroupEndPattern).FindString(line); match != "" {
				project.Configs[utility.ToConfig(configurationPlatform.Configuration, configurationPlatform.Platform)] = configurationPlatform

				configurationPlatform = ConfigurationPlatformModel{}

				isPropertyGroupSection = false
				continue
			}
		}

		// PropertyGroup with Condition (Configuration & Platform)
		if matches := regexp.MustCompile(propertyGroupWithConditionConfigurationAndPlatformPattern).FindStringSubmatch(line); len(matches) == 3 {
			platform := matches[2]
			/*
				if platform == "AnyCPU" {
					platform = "Any CPU"
				}
			*/

			configurationPlatform = ConfigurationPlatformModel{
				Configuration: matches[1],
				Platform:      platform,
			}

			isPropertyGroupSection = true
			continue
		}

		// PropertyGroup with Condition (Configuration)
		if matches := regexp.MustCompile(propertyGroupWithConditionConfigurationPattern).FindStringSubmatch(line); len(matches) == 2 {
			configurationPlatform = ConfigurationPlatformModel{
				Configuration: matches[1],
			}

			isPropertyGroupSection = true
			continue
		}

		// PropertyGroup with Condition (Platform)
		if matches := regexp.MustCompile(propertyGroupWithConditionPlatformPattern).FindStringSubmatch(line); len(matches) == 2 {
			platform := matches[2]
			/*
				if platform == "AnyCPU" {
					platform = "Any CPU"
				}
			*/

			configurationPlatform = ConfigurationPlatformModel{
				Platform: platform,
			}

			isPropertyGroupSection = true
			continue
		}

		if isPropertyGroupSection {
			// OutputPath
			if matches := regexp.MustCompile(outputPathPattern).FindStringSubmatch(line); len(matches) == 2 {
				outputRelativePth := utility.FixWindowsPath(matches[1])
				strings.Replace(outputRelativePth, "$(Configuration)", configurationPlatform.Configuration, -1)
				strings.Replace(outputRelativePth, "$(Platform)", configurationPlatform.Platform, -1)

				configurationPlatform.OutputDir = filepath.Join(projectDir, outputRelativePth)
				continue
			}

			// MtouchArch
			if matches := regexp.MustCompile(mtouchArchPattern).FindStringSubmatch(line); len(matches) == 2 {
				configurationPlatform.MtouchArchs = utility.SplitAndStripList(matches[1], ",")
				continue
			}

			// AndroidKeyStore
			if match := regexp.MustCompile(androidKeystorePattern).FindString(line); match != "" {
				configurationPlatform.SignAndroid = true
				continue
			}

			/*
				// IpaPackageName
				if match := regexp.MustCompile(ipaPackageNamePattern).FindString(line); match != "" {
					configurationPlatform.IpaPackage = true
					continue
				}
			*/

			// BuildIpa ...
			if match := regexp.MustCompile(buildIpaPattern).FindString(line); match != "" {
				configurationPlatform.BuildIpa = true
				continue
			}
		}

		//
		// API

		// ProjectTypeGuids
		if matches := regexp.MustCompile(typeGUIDsPattern).FindStringSubmatch(line); len(matches) == 2 {
			projectType := constants.ProjectTypeUnknown
			projectTypeList := strings.Split(matches[1], ";")
			for _, guid := range projectTypeList {
				guid = strings.TrimPrefix(guid, "{")
				guid = strings.TrimSuffix(guid, "}")

				projectType, err = constants.ParseProjectTypeGUID(guid)
				if err == nil {
					break
				}
			}

			project.ProjectType = projectType
			continue
		}

		if match := regexp.MustCompile(referenceXamarinUITestPattern).FindString(line); match != "" {
			project.TestFrameworks = append(project.TestFrameworks, constants.TestFrameworkXamarinUITest)
			continue
		}

		if match := regexp.MustCompile(referenceNunitFramework).FindString(line); match != "" {
			project.TestFrameworks = append(project.TestFrameworks, constants.TestFrameworkNunitTest)
			continue
		}

		if match := regexp.MustCompile(referenceNunitLiteFramework).FindString(line); match != "" {
			project.TestFrameworks = append(project.TestFrameworks, constants.TestFrameworkNunitLiteTest)
			continue
		}

		//
		// ProjectReference

		if isProjectReferenceSection {
			if match := regexp.MustCompile(projectRefernceEndPattern).FindString(line); match != "" {
				isProjectReferenceSection = false
			}
		}

		// ProjectReference
		if matches := regexp.MustCompile(projectRefernceStartPattern).FindStringSubmatch(line); len(matches) == 2 {
			/*
				projectRelativePth := fixWindowsPath(matches[1])
				projectPth := filepath.Join(projectDir, projectRelativePth)
			*/

			isProjectReferenceSection = true
			continue
		}

		if isProjectReferenceSection {
			if matches := regexp.MustCompile(referredProjectIDPattern).FindStringSubmatch(line); len(matches) == 2 {
				project.ReferredProjectIDs = append(project.ReferredProjectIDs, matches[1])
			}
			continue
		}

	}
	if err := scanner.Err(); err != nil {
		return Model{}, err
	}

	// Set Project Test Project Type
	includesXamarinUITestFramwork := false
	includesNunitTestFramework := false
	includesNunitLiteTestFramwork := false

	for _, testFramework := range project.TestFrameworks {
		if testFramework == constants.TestFrameworkXamarinUITest {
			includesXamarinUITestFramwork = true
		}
		if testFramework == constants.TestFrameworkNunitTest {
			includesNunitTestFramework = true
		}
		if testFramework == constants.TestFrameworkNunitLiteTest {
			includesNunitLiteTestFramwork = true
		}
	}

	if includesXamarinUITestFramwork {
		project.ProjectType = constants.ProjectTypeXamarinUITest
	} else if includesNunitTestFramework {
		project.ProjectType = constants.ProjectTypeNunitTest
	}

	if includesNunitLiteTestFramwork {
		project.ProjectType = constants.ProjectTypeNunitLiteTest
	}

	return project, nil
}

func analyzeProject(pth string) (Model, error) {
	absPth, err := pathutil.AbsPath(pth)
	if err != nil {
		return Model{}, fmt.Errorf("Failed to expand path (%s), error: %s", pth, err)
	}

	fileName := filepath.Base(absPth)
	ext := filepath.Ext(absPth)
	fileName = strings.TrimSuffix(fileName, ext)

	project := Model{
		Pth:       absPth,
		Name:      fileName,
		ConfigMap: map[string]string{},
		Configs:   map[string]ConfigurationPlatformModel{},
	}
	return analyzeTargetDefinition(project, absPth)
}
