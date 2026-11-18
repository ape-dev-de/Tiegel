package api

import (
	"context"
	"regexp"
	"strings"

	"github.com/ape-dev-de/tiegel/internal/core/settings"
)

// File templates (AP17.3): the kernel renders new PHP files (New › PHP
// Class/Interface/Trait/Enum/Test) from templates with ${VARIABLE}
// placeholders. Each template can be replaced by the user with the core
// setting fileTemplates.<kind> (project over global; empty = built-in).

// FileTemplate is a built-in file template.
type FileTemplate struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Default is the built-in text.
	Default string `json:"default"`
}

const phpHeader = "<?php\n\ndeclare(strict_types=1);\n\nnamespace ${NAMESPACE};\n\n"

// fileTemplates are the built-in templates in menu order.
var fileTemplates = []FileTemplate{
	{Kind: "class", Label: "Class (final)", Default: phpHeader + "final class ${NAME}\n{\n}\n"},
	{Kind: "abstract", Label: "Abstract Class", Default: phpHeader + "abstract class ${NAME}\n{\n}\n"},
	{Kind: "interface", Label: "Interface", Default: phpHeader + "interface ${NAME}\n{\n}\n"},
	{Kind: "trait", Label: "Trait", Default: phpHeader + "trait ${NAME}\n{\n}\n"},
	{Kind: "enum", Label: "Enum", Default: phpHeader + "enum ${NAME}\n{\n}\n"},
	{Kind: "test", Label: "Test", Default: phpHeader + "use ${TEST_BASE_USE};\n\nfinal class ${NAME} extends ${TEST_BASE_NAME}\n{\n" +
		"    public function testSomething(): void\n    {\n        self::markTestIncomplete('Write the first test.');\n    }\n}\n"},
}

// FileTemplateKinds returns the template kinds in menu order.
func FileTemplateKinds() []string {
	out := make([]string, len(fileTemplates))
	for i, t := range fileTemplates {
		out[i] = t.Kind
	}
	return out
}

func fileTemplateByKind(kind string) *FileTemplate {
	for i := range fileTemplates {
		if fileTemplates[i].Kind == kind {
			return &fileTemplates[i]
		}
	}
	return nil
}

// SettingFileTemplatePrefix starts the keys of user file templates
// (fileTemplates.<kind>).
const SettingFileTemplatePrefix = "fileTemplates."

// fileTemplateVarsHelp lists the variables for the setting help.
const fileTemplateVarsHelp = "${NAME}, ${NAMESPACE}, ${FQN}, ${DATE}, ${YEAR}, ${USER}; tests also ${TEST_BASE}, ${TEST_BASE_NAME}, ${TEST_BASE_USE}"

var fileTemplateSpecsErr = settings.RegisterCore(fileTemplateSpecs()...)

func fileTemplateSpecs() []settings.Spec {
	out := make([]settings.Spec, 0, len(fileTemplates))
	for _, t := range fileTemplates {
		out = append(out, settings.Spec{Key: SettingFileTemplatePrefix + t.Kind, Kind: settings.KindString, Default: "", Scope: settings.ScopeProject,
			Label: "New PHP " + t.Label, Group: "File Templates",
			Help: `Replaces the built-in template (empty: built-in). Write \n for a line break. Variables: ` + fileTemplateVarsHelp +
				`. A line with a variable that is empty (no namespace, no import needed) is left out.`})
	}
	return out
}

// FileTemplateInfo describes a template for files.templates.
type FileTemplateInfo struct {
	FileTemplate
	// Setting is the key of the user template.
	Setting string `json:"setting"`
	// Custom is true when the user template replaces the built-in one.
	Custom bool `json:"custom"`
	// Template is the effective text.
	Template string `json:"template"`
}

// templateText returns the effective template of kind: the user template
// (literal \n become line breaks when it has no real ones) or the default;
// custom reports the user template.
func (a *App) templateText(kind string) (string, bool) {
	t := fileTemplateByKind(kind)
	if t == nil {
		return "", false
	}
	user := Setting(a, SettingFileTemplatePrefix+kind, "")
	if strings.TrimSpace(user) == "" {
		return t.Default, false
	}
	if !strings.Contains(user, "\n") {
		user = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\\`, `\`).Replace(user)
	}
	if !strings.HasSuffix(user, "\n") {
		user += "\n"
	}
	return user, true
}

var templateVar = regexp.MustCompile(`\$\{([A-Z_]+)\}`)

// renderFileTemplate fills the template of kind. Lines that contain a
// variable with an empty value are dropped (with a blank line after them
// when the line before is blank too).
func (a *App) renderFileTemplate(kind string, vars map[string]string) (string, error) {
	if fileTemplateByKind(kind) == nil {
		return "", Errorf(CodeInvalidParams, "unknown file template %q", kind)
	}
	text, _ := a.templateText(kind)
	return renderTemplate(text, vars), nil
}

func renderTemplate(text string, vars map[string]string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	dropBlank := false
	for _, line := range lines {
		if dropBlank && strings.TrimSpace(line) == "" {
			dropBlank = false
			continue
		}
		dropBlank = false
		empty := false
		line = templateVar.ReplaceAllStringFunc(line, func(m string) string {
			v, known := vars[m[2:len(m)-1]]
			if !known {
				return m
			}
			if v == "" {
				empty = true
			}
			return v
		})
		if empty {
			dropBlank = len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == ""
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func (a *App) filesTemplates(context.Context, struct{}) ([]FileTemplateInfo, error) {
	out := make([]FileTemplateInfo, 0, len(fileTemplates))
	for _, t := range fileTemplates {
		text, custom := a.templateText(t.Kind)
		out = append(out, FileTemplateInfo{FileTemplate: t, Setting: SettingFileTemplatePrefix + t.Kind, Custom: custom, Template: text})
	}
	return out, nil
}
