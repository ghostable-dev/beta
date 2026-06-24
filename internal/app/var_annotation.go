package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/store"
)

var variableAnnotationCommandOptions = []commandOption{
	{Label: "list", Description: "List annotations for a key"},
	{Label: "set", Description: "Set a string, number, or bool annotation"},
	{Label: "remove", Description: "Remove an annotation from a key"},
}

type variableAnnotationMutationResult struct {
	Environment string              `json:"environment"`
	Key         string              `json:"key"`
	Annotation  store.KeyAnnotation `json:"annotation"`
	Removed     bool                `json:"removed,omitempty"`
}

func (r *Runner) runVarAnnotation(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printVarAnnotationHelp()
			return nil
		}
		selected, err := r.selectCommand("Select an annotation command", variableAnnotationCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printVarAnnotationHelp()
		return nil
	}

	switch args[0] {
	case "list", "show":
		return r.runVarAnnotationList(args[1:])
	case "set", "put":
		return r.runVarAnnotationSet(args[1:])
	case "remove", "delete", "unset":
		return r.runVarAnnotationRemove(args[1:])
	default:
		return fmt.Errorf("unknown var annotation command %q", args[0])
	}
}

func (r *Runner) printVarAnnotationHelp() {
	fmt.Fprintln(r.out, "Usage: ghostable var annotation <command> [options]")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, variableAnnotationCommandOptions)
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "Examples:")
	fmt.Fprintln(r.out, "  ghostable var annotation set --env production --key APP_KEY --name owner --string platform")
	fmt.Fprintln(r.out, "  ghostable var annotation set --env production --key APP_KEY --name rotation_days --number 90")
	fmt.Fprintln(r.out, "  ghostable var annotation set --env production --key APP_KEY --name deploy.managed --bool true")
	fmt.Fprintln(r.out, "  ghostable var annotation list --env production --key APP_KEY")
}

func (r *Runner) runVarAnnotationList(args []string) error {
	fs := newFlagSet("var annotation list", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	jsonOut := fs.Bool("json", false, "Print annotations as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	selected, err := r.selectEnvironment(repo, *env)
	if err != nil {
		return err
	}
	variableKey, err := r.selectAnnotationKey(repo, selected, *key, false)
	if err != nil {
		return err
	}
	result, err := repo.ReadKeyAnnotations(selected, variableKey)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	if len(result.Annotations) == 0 {
		fmt.Fprintln(r.out, warn(fmt.Sprintf("%s has no annotations.", variableKey)))
		return nil
	}
	for _, annotation := range result.Annotations {
		fmt.Fprintf(r.out, "%s=%s\n", annotation.Name, formatKeyAnnotationValue(annotation.Value))
	}
	return nil
}

func (r *Runner) runVarAnnotationSet(args []string) error {
	fs := newFlagSet("var annotation set", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	name := fs.String("name", "", "Annotation name")
	stringValue := fs.String("string", "", "String annotation value")
	numberValue := fs.String("number", "", "Number annotation value")
	boolValue := fs.String("bool", "", "Boolean annotation value")
	jsonOut := fs.Bool("json", false, "Print annotation as JSON")
	stringProvided := hasFlag(args, "string")
	numberProvided := hasFlag(args, "number")
	boolProvided := hasFlag(args, "bool")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	if annotationValueFlagCount(stringProvided, numberProvided, boolProvided) > 1 {
		return fmt.Errorf("pass only one of --string, --number, or --bool")
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	selected, err := r.selectEnvironment(repo, *env)
	if err != nil {
		return err
	}
	variableKey, err := r.selectAnnotationKey(repo, selected, *key, true)
	if err != nil {
		return err
	}
	annotationName, err := r.annotationName(*name)
	if err != nil {
		return err
	}
	value, err := r.annotationValue(*stringValue, *numberValue, *boolValue, stringProvided, numberProvided, boolProvided)
	if err != nil {
		return err
	}
	annotation, err := repo.SetKeyAnnotation(selected, variableKey, annotationName, value)
	if err != nil {
		return err
	}
	result := variableAnnotationMutationResult{
		Environment: selected,
		Key:         variableKey,
		Annotation:  annotation,
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	fmt.Fprintf(r.out, "%s %s=%s on %s in %s.\n", success("Set"), annotation.Name, formatKeyAnnotationValue(annotation.Value), variableKey, selected)
	return nil
}

func (r *Runner) runVarAnnotationRemove(args []string) error {
	fs := newFlagSet("var annotation remove", r.errOut)
	env := fs.String("env", "", "Environment name")
	key := fs.String("key", "", "Variable name")
	name := fs.String("name", "", "Annotation name")
	jsonOut := fs.Bool("json", false, "Print removed annotation as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	selected, err := r.selectEnvironment(repo, *env)
	if err != nil {
		return err
	}
	variableKey, err := r.selectAnnotationKey(repo, selected, *key, false)
	if err != nil {
		return err
	}
	annotationName, err := r.annotationNameForRemoval(repo, selected, variableKey, *name)
	if err != nil {
		return err
	}
	annotation, err := repo.RemoveKeyAnnotation(selected, variableKey, annotationName)
	if err != nil {
		return err
	}
	result := variableAnnotationMutationResult{
		Environment: selected,
		Key:         variableKey,
		Annotation:  annotation,
		Removed:     true,
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	fmt.Fprintf(r.out, "%s %s from %s in %s.\n", success("Removed"), annotation.Name, variableKey, selected)
	return nil
}

func (r *Runner) selectAnnotationKey(repo store.Repository, env string, provided string, allowNew bool) (string, error) {
	if provided != "" {
		key, _, err := formatManualVariableKey(provided)
		return key, err
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --key")
	}

	keys, err := repo.ReadKeyMetadataKeys(env)
	if err != nil {
		return "", err
	}
	if len(keys) == 0 {
		if allowNew {
			return r.askManualVariableKey()
		}
		return "", fmt.Errorf("no keys are stored in %s", env)
	}
	if allowNew {
		keys = append(keys, "New key")
	}
	selected, err := r.prompts.Select("Select a key", keys, 0)
	if err != nil {
		return "", err
	}
	if selected == "New key" {
		return r.askManualVariableKey()
	}
	return selected, nil
}

func (r *Runner) annotationName(provided string) (string, error) {
	provided = strings.TrimSpace(provided)
	if provided != "" {
		return provided, nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --name")
	}
	return r.prompts.Ask("Annotation name", "")
}

func (r *Runner) annotationNameForRemoval(repo store.Repository, env string, key string, provided string) (string, error) {
	provided = strings.TrimSpace(provided)
	if provided != "" {
		return provided, nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --name")
	}
	result, err := repo.ReadKeyAnnotations(env, key)
	if err != nil {
		return "", err
	}
	if len(result.Annotations) == 0 {
		return "", fmt.Errorf("%s has no annotations", key)
	}
	choices := make([]string, 0, len(result.Annotations))
	for _, annotation := range result.Annotations {
		choices = append(choices, annotation.Name)
	}
	return r.prompts.Select("Select an annotation", choices, 0)
}

func annotationValueFlagCount(flags ...bool) int {
	count := 0
	for _, flag := range flags {
		if flag {
			count++
		}
	}
	return count
}

func (r *Runner) annotationValue(stringValue string, numberValue string, boolValue string, stringProvided bool, numberProvided bool, boolProvided bool) (domain.KeyAnnotationValue, error) {
	switch {
	case stringProvided:
		return store.NewStringKeyAnnotation(stringValue), nil
	case numberProvided:
		number, err := strconv.ParseFloat(strings.TrimSpace(numberValue), 64)
		if err != nil {
			return domain.KeyAnnotationValue{}, fmt.Errorf("invalid number annotation value %q: %w", numberValue, err)
		}
		return store.NewNumberKeyAnnotation(number), nil
	case boolProvided:
		parsedBool, err := parseAnnotationBool(boolValue)
		if err != nil {
			return domain.KeyAnnotationValue{}, err
		}
		return store.NewBoolKeyAnnotation(parsedBool), nil
	case r.interactive:
		annotationType, err := r.prompts.Select("Annotation type", []string{"string", "number", "bool"}, 0)
		if err != nil {
			return domain.KeyAnnotationValue{}, err
		}
		value, err := r.prompts.Ask("Annotation value", "")
		if err != nil {
			return domain.KeyAnnotationValue{}, err
		}
		switch annotationType {
		case domain.KeyAnnotationNumber:
			number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				return domain.KeyAnnotationValue{}, fmt.Errorf("invalid number annotation value %q: %w", value, err)
			}
			return store.NewNumberKeyAnnotation(number), nil
		case domain.KeyAnnotationBool:
			parsedBool, err := parseAnnotationBool(value)
			if err != nil {
				return domain.KeyAnnotationValue{}, err
			}
			return store.NewBoolKeyAnnotation(parsedBool), nil
		default:
			return store.NewStringKeyAnnotation(value), nil
		}
	default:
		return domain.KeyAnnotationValue{}, fmt.Errorf("pass --string, --number, or --bool")
	}
}

func parseAnnotationBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "t", "yes", "y", "1", "on":
		return true, nil
	case "false", "f", "no", "n", "0", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool annotation value %q", value)
	}
}

func formatKeyAnnotationValue(value domain.KeyAnnotationValue) string {
	switch value.Type {
	case domain.KeyAnnotationString:
		if value.String == nil {
			return ""
		}
		return strconv.Quote(*value.String)
	case domain.KeyAnnotationNumber:
		if value.Number == nil {
			return ""
		}
		return strconv.FormatFloat(*value.Number, 'f', -1, 64)
	case domain.KeyAnnotationBool:
		if value.Bool == nil {
			return ""
		}
		return strconv.FormatBool(*value.Bool)
	default:
		return "<invalid>"
	}
}
