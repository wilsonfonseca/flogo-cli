package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/TIBCOSoftware/flogo-cli/cli"
	"github.com/TIBCOSoftware/flogo-cli/util"
	"net/url"
	"net/http"
	"io/ioutil"
	"strconv"
	"github.com/TIBCOSoftware/flogo-cli/config"
)

var optAdd = &cli.OptionInfo{
	Name:      "add",
	UsageLine: "add [-src] [-v version[ [-b branch] <activity|model|trigger|flow> <path>",
	Short:     "add an activity, flow, model, trigger or palette to a flogo project",
	Long: `Add an activity, flow, model, trigger or palette to a flogo project

Options:
    -src        copy contents to source (only when using file url)
    -v version  specifiy the version (resolves to a git tag with format 'v'{version}
    -b branch   specify the branch to use
`,
}

var validItemTypes = []string{itActivity, itTrigger, itModel, itFlow, itPalette}

func init() {
	commandRegistry.RegisterCommand(&cmdAdd{option: optAdd})
}

type cmdAdd struct {
	option   *cli.OptionInfo
	addToSrc bool
	version  string
	branch   string
}

func (c *cmdAdd) OptionInfo() *cli.OptionInfo {
	return c.option
}

func (c *cmdAdd) AddFlags(fs *flag.FlagSet) {
	fs.BoolVar(&(c.addToSrc), "src", false, "copy contents to source (only when using local/file)")
	fs.StringVar(&(c.version), "v", "", "version")
	fs.StringVar(&(c.branch), "b", "", "branch")
}

func (c *cmdAdd) Exec(args []string) error {

	projectDescriptor := config.LoadProjectDescriptor()

	if len(args) == 0 {
		fmt.Fprint(os.Stderr, "Error: item type not specified\n\n")
		cmdUsage(c)
	}

	itemType := strings.ToLower(args[0])

	if !fgutil.IsStringInList(itemType, validItemTypes) {
		fmt.Fprintf(os.Stderr, "Error: invalid item type '%s'\n\n", itemType)
		cmdUsage(c)
	}

	if len(args) == 1 {
		fmt.Fprintf(os.Stderr, "Error: %s path not specified\n\n", fgutil.Capitalize(itemType))
		cmdUsage(c)
	}

	itemPath := args[1]

	if len(args) > 2 {
		fmt.Fprint(os.Stderr, "Error: Too many arguments given\n\n")
		cmdUsage(c)
	}

	label := ""
	isBranch := false

	if c.branch != "" {
		label = c.branch
		isBranch = true

	} else {

		label = c.version

		idx := strings.LastIndex(itemPath, "@")

		if idx > -1 {
			v := itemPath[idx + 1:]

			if isValidVersion(v) {
				label = v
				itemPath = itemPath[0:idx]
			}
		}
	}

	installItem(projectDescriptor, itemType, itemPath, label, isBranch, c.addToSrc)

	return nil
}

//todo validate that "s" a valid semver
func isValidVersion(s string) bool {

	if s == "" {
		//assume latest version
		return true
	}

	if s[0] == 'v' && len(s) > 1 && isNumeric(string(s[1])) {
		return true
	}

	if isNumeric(string(s[0])) {
		return true
	}

	return false;
}

func isNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func installItem(projectDescriptor *config.FlogoProjectDescriptor, itemType string, itemPath string, label string, isBranch bool, addToSrc bool) {

	gb := fgutil.NewGb(projectDescriptor.Name)

	updateFiles := true

	switch itemType {
	case itActivity:
		addActivity(gb, projectDescriptor, itemPath, label, isBranch, addToSrc, false)
	case itModel:
		addModel(gb, projectDescriptor, itemPath, label, isBranch, addToSrc, false)
	case itTrigger:
		addTrigger(gb, projectDescriptor, itemPath, label, isBranch, addToSrc, false)
	case itFlow:
		updateFiles = false
		addFlow(gb, projectDescriptor, itemPath, addToSrc)
	case itPalette:
		addPalette(gb, projectDescriptor, itemPath, addToSrc)
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown item type '%s'\n\n", itemType)
		os.Exit(2)
	}

	if updateFiles {
		updateProjectFiles(gb, projectDescriptor)
	}
}

func addActivity(gb *fgutil.Gb, projectDescriptor *config.FlogoProjectDescriptor, itemPath string, label string, isBranch bool, addToSrc bool, ignoreDup bool) {

	var itemConfig *config.ItemDescriptor

	itemConfig, _ = AddFlogoItem(gb, itActivity, itemPath, label, isBranch, projectDescriptor.Activities, addToSrc, ignoreDup)

	if itemConfig != nil {
		projectDescriptor.Activities = append(projectDescriptor.Activities, itemConfig)
		fmt.Fprintf(os.Stdout, "Added Activity: %s [%s]\n", itemConfig.Name, itemConfig.Path)
	}
}

func addModel(gb *fgutil.Gb, projectDescriptor *config.FlogoProjectDescriptor, itemPath string, label string, isBranch bool, addToSrc bool, ignoreDup bool) {

	var itemConfig *config.ItemDescriptor

	itemConfig, _ = AddFlogoItem(gb, itModel, itemPath, label, isBranch, projectDescriptor.Models, addToSrc, ignoreDup)
	if itemConfig != nil {
		projectDescriptor.Models = append(projectDescriptor.Models, itemConfig)
		fmt.Fprintf(os.Stdout, "Added Model: %s [%s]\n", itemConfig.Name, itemConfig.Path)
	}
}

func addTrigger(gb *fgutil.Gb, projectDescriptor *config.FlogoProjectDescriptor, itemPath string, label string, isBranch bool, addToSrc bool, ignoreDup bool) {

	var itemConfig *config.ItemDescriptor
	var itemConfigPath string

	itemConfig, itemConfigPath = AddFlogoItem(gb, itTrigger, itemPath, label, isBranch, projectDescriptor.Triggers, addToSrc, ignoreDup)

	if itemConfig == nil {
		return
	}

	projectDescriptor.Triggers = append(projectDescriptor.Triggers, itemConfig)

	//read trigger.json
	triggerConfigFile, err := os.Open(itemConfigPath)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Unable to find '%s'\n\n", itemConfigPath)
		os.Exit(2)
	}

	triggerProjectDescriptor := &config.TriggerProjectDescriptor{}
	jsonParser := json.NewDecoder(triggerConfigFile)

	if err = jsonParser.Decode(triggerProjectDescriptor); err != nil {
		fmt.Fprint(os.Stderr, "Error: Unable to parse trigger.json, file may be corrupted.\n\n")
		os.Exit(2)
	}

	triggerConfigFile.Close()

	//read triggers.json
	triggersConfigPath := gb.NewBinFilePath(fileTriggersConfig)
	triggersConfigFile, err := os.Open(triggersConfigPath)

	triggersConfig := &config.TriggersConfig{}
	jsonParser = json.NewDecoder(triggersConfigFile)

	if err = jsonParser.Decode(triggersConfig); err != nil {
		fmt.Fprint(os.Stderr, "Error: Unable to parse application triggers.json, file may be corrupted.\n\n")
		os.Exit(2)
	}

	triggersConfigFile.Close()

	if triggersConfig.Triggers == nil {
		triggersConfig.Triggers = make([]*config.TriggerConfig, 0)
	}

	if !ContainsTriggerConfig(triggersConfig.Triggers, itemConfig.Name) {

		triggerConfig := &config.TriggerConfig{Name: itemConfig.Name, Settings: make(map[string]string)}

		for _, v := range triggerProjectDescriptor.Settings {

			triggerConfig.Settings[v.Name] = v.Value
		}

		triggersConfig.Triggers = append(triggersConfig.Triggers, triggerConfig)

		fgutil.WriteJSONtoFile(triggersConfigPath, triggersConfig)
	}

	fmt.Fprintf(os.Stdout, "Added Trigger: %s [%s]\n", itemConfig.Name, itemConfig.Path)
}

func addFlow(gb *fgutil.Gb, projectDescriptor *config.FlogoProjectDescriptor, itemPath string, addToSrc bool) {

	pathInfo, err := fgutil.GetPathInfo(itemPath)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Invalid path '%s'\n", itemPath)
		os.Exit(2)
	}

	if pathInfo.IsLocal {

		if !pathInfo.IsFile {
			fmt.Fprintf(os.Stderr, "Error: Path '%s' is not a file\n", itemPath)
			os.Exit(2)
		}
	}

	ValidateFlow(projectDescriptor, itemPath, pathInfo.IsURL)

	if (pathInfo.IsLocal) {
		fgutil.CopyFile(pathInfo.FilePath, path("flows", pathInfo.FileName))
	} else if (pathInfo.IsURL) {
		fgutil.CopyRemoteFile(pathInfo.FileURL.String(), path("flows", pathInfo.FileName))
	}

	flows := ImportFlows(projectDescriptor, dirFlows)
	createFlowsGoFile(gb.CodeSourcePath, flows)
}

func addPalette(gb *fgutil.Gb, projectDescriptor *config.FlogoProjectDescriptor, itemPath string, addToSrc bool) {

	pathInfo, err := fgutil.GetPathInfo(itemPath)

	if err != nil || (!pathInfo.IsLocal && !pathInfo.IsURL) {
		fmt.Fprintf(os.Stderr, "Error: Invalid path '%s'\n", itemPath)
		os.Exit(2)
	}

	if pathInfo.IsLocal {

		if !pathInfo.IsFile {
			fmt.Fprintf(os.Stderr, "Error: Path '%s' is not a file\n", itemPath)
			os.Exit(2)
		}
	}

	var file []byte

	if pathInfo.IsURL {

		flowURL, _ := url.Parse(itemPath)
		flowFilePath, local := fgutil.URLToFilePath(flowURL)

		if !local {
			resp, err := http.Get(flowURL.String())
			defer resp.Body.Close()

			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: Unable to access '%s'\n  - %s\n", flowURL.String(), err.Error())
				os.Exit(2)
			}

			file, _ = ioutil.ReadAll(resp.Body)

		} else {
			file, _ = ioutil.ReadFile(flowFilePath)
		}

	} else {
		file, _ = ioutil.ReadFile(itemPath)
	}

	var paletteDescriptor *config.FlogoPaletteDescriptor
	err = json.Unmarshal(file, &paletteDescriptor)

	if err != nil {
		fmt.Fprint(os.Stderr, "Error: Unable to parse palette descriptor, file may be corrupted.\n\n")
		os.Exit(2)
	}

	fmt.Fprintf(os.Stdout, "Adding Palette: %s [%s]\n\n", paletteDescriptor.Name, projectDescriptor.Description)

	activities := paletteDescriptor.FlogoExtensions.Activities

	for _, activity := range activities {
		addActivity(gb, projectDescriptor, activity.Path, activity.Version, false, true, true)
	}

	triggers := paletteDescriptor.FlogoExtensions.Triggers

	for _, trigger := range triggers {
		addTrigger(gb, projectDescriptor, trigger.Path, trigger.Version, false, true, true)
	}
}

// ContainsTriggerConfig determines if the list of TriggerConfigs contains the specified one
func ContainsTriggerConfig(list []*config.TriggerConfig, triggerName string) bool {
	for _, v := range list {
		if v.Name == triggerName {
			return true
		}
	}
	return false
}