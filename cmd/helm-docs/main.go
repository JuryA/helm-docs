package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"golang.org/x/sync/errgroup"

	"cuelang.org/go/cue/cuecontext"
	"github.com/norwoodj/helm-docs/pkg/cueutil"
	"github.com/norwoodj/helm-docs/pkg/document"
	"github.com/norwoodj/helm-docs/pkg/helm"
)

// parallelProcessIterable runs the visitFn function on each element of the iterable, using
// parallelism number of worker goroutines. The iterable may be a slice or a map. In the case of a
// map, the argument passed to visitFn will be the key.
func parallelProcessIterable(iterable interface{}, parallelism int, visitFn func(elem interface{})) {
	workChan := make(chan interface{})

	wg := &sync.WaitGroup{}
	wg.Add(parallelism)

	for i := 0; i < parallelism; i++ {
		go func() {
			defer wg.Done()
			for elem := range workChan {
				visitFn(elem)
			}
		}()
	}

	iterableValue := reflect.ValueOf(iterable)

	if iterableValue.Kind() == reflect.Map {
		for _, key := range iterableValue.MapKeys() {
			workChan <- key.Interface()
		}
	} else {
		sliceLen := iterableValue.Len()
		for i := 0; i < sliceLen; i++ {
			workChan <- iterableValue.Index(i).Interface()
		}
	}

	close(workChan)
	wg.Wait()
}

func validateSchemaOutputPath(p string) (string, error) {
	clean := filepath.Clean(p)
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("schema output must be a relative path without directory traversal")
	}
	rel, err := filepath.Rel(".", clean)
	if err != nil || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", fmt.Errorf("schema output must be a relative path without directory traversal")
	}
	return clean, nil
}

func joinChartOutputPath(chartRoot, chartDir, cleanOutput string) (string, error) {
	base := filepath.Join(chartRoot, chartDir)
	out := filepath.Join(base, cleanOutput)
	rel, err := filepath.Rel(base, out)
	if err != nil || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", fmt.Errorf("resolved output path escapes chart directory")
	}
	return out, nil
}

func writeSchemaFile(outPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("creating schema directory: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("writing schema: %w", err)
	}
	return nil
}

func generateSchema(info helm.ChartDocumentationInfo) ([]byte, error) {
	ctx := cuecontext.New()
	data, err := cueutil.GenerateJSONSchemaFromYAML(ctx, info.ChartValues)
	if err != nil {
		if errors.Is(err, cueutil.ErrIncompleteSchema) {
			err = fmt.Errorf("schema is incomplete: %w", err)
		}
		return nil, err
	}
	return data, nil
}

func getDocumentationParsingConfigFromArgs() (helm.ChartValuesDocumentationParsingConfig, error) {
	var regexps []*regexp.Regexp
	regexpStrings := viper.GetStringSlice("documentation-strict-ignore-absent-regex")
	for _, item := range regexpStrings {
		regex, err := regexp.Compile(item)
		if err != nil {
			return helm.ChartValuesDocumentationParsingConfig{}, err
		}
		regexps = append(regexps, regex)
	}
	return helm.ChartValuesDocumentationParsingConfig{
		StrictMode:                 viper.GetBool("documentation-strict-mode"),
		AllowedMissingValuePaths:   viper.GetStringSlice("documentation-strict-ignore-absent"),
		AllowedMissingValueRegexps: regexps,
	}, nil
}

func readDocumentationInfoByChartPath(chartSearchRoot string, parallelism int) (map[string]helm.ChartDocumentationInfo, error) {
	var fullChartSearchRoot string

	if path.IsAbs(chartSearchRoot) {
		fullChartSearchRoot = chartSearchRoot
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("error getting working directory: %w", err)
		}

		fullChartSearchRoot = filepath.Join(cwd, chartSearchRoot)
	}

	chartDirs, err := helm.FindChartDirectories(fullChartSearchRoot)
	if err != nil {
		return nil, fmt.Errorf("error finding chart directories: %w", err)
	}

	log.Infof("Found Chart directories [%s]", strings.Join(chartDirs, ", "))

	templateFiles := viper.GetStringSlice("template-files")
	log.Debugf("Rendering from optional template files [%s]", strings.Join(templateFiles, ", "))

	documentationInfoByChartPath := make(map[string]helm.ChartDocumentationInfo, len(chartDirs))
	documentationInfoByChartPathMu := &sync.Mutex{}
	documentationParsingConfig, err := getDocumentationParsingConfigFromArgs()
	if err != nil {
		return nil, fmt.Errorf("error parsing the linting config%w", err)
	}

	parallelProcessIterable(chartDirs, parallelism, func(elem interface{}) {
		chartDir := elem.(string)
		info, err := helm.ParseChartInformation(filepath.Join(chartSearchRoot, chartDir), documentationParsingConfig)
		if err != nil {
			log.Warnf("Error parsing information for chart %s, skipping: %s", chartDir, err)
			return
		}
		documentationInfoByChartPathMu.Lock()
		documentationInfoByChartPath[info.ChartDirectory] = info
		documentationInfoByChartPathMu.Unlock()
	})

	return documentationInfoByChartPath, nil
}

func getChartToGenerate(documentationInfoByChartPath map[string]helm.ChartDocumentationInfo) map[string]helm.ChartDocumentationInfo {
	generateDirectories := viper.GetStringSlice("chart-to-generate")
	if len(generateDirectories) == 0 {
		return documentationInfoByChartPath
	}
	documentationInfoToGenerate := make(map[string]helm.ChartDocumentationInfo, len(generateDirectories))
	var skipped = false
	for _, chartDirectory := range generateDirectories {
		if info, ok := documentationInfoByChartPath[chartDirectory]; ok {
			documentationInfoToGenerate[chartDirectory] = info
		} else {
			log.Warnf("Couldn't find documentation Info for <%s> - skipping", chartDirectory)
			skipped = true
		}
	}
	if skipped {
		possibleCharts := []string{}
		for path := range documentationInfoByChartPath {
			possibleCharts = append(possibleCharts, path)
		}
		log.Warnf("Some charts listed in `chart-to-generate` wasn't found. List of charts to choose: [%s]", strings.Join(possibleCharts, ", "))
	}
	return documentationInfoToGenerate
}

func writeDocumentation(chartSearchRoot string, documentationInfoByChartPath map[string]helm.ChartDocumentationInfo, dryRun bool, parallelism int) {
	templateFiles := viper.GetStringSlice("template-files")
	badgeStyle := viper.GetString("badge-style")
	skipVersionFooter := viper.GetBool("skip-version-footer")

	log.Debugf("Rendering from optional template files [%s]", strings.Join(templateFiles, ", "))

	documentDependencyValues := viper.GetBool("document-dependency-values")
	documentationInfoToGenerate := getChartToGenerate(documentationInfoByChartPath)

	parallelProcessIterable(documentationInfoToGenerate, parallelism, func(elem interface{}) {
		info := documentationInfoByChartPath[elem.(string)]
		var err error
		var dependencyValues []document.DependencyValues
		if documentDependencyValues {
			dependencyValues, err = document.GetDependencyValues(info, documentationInfoByChartPath)
			if err != nil {
				log.Warnf("Error evaluating dependency values for chart %s, skipping: %v", info.ChartDirectory, err)
				return
			}
		}
		document.PrintDocumentation(info, chartSearchRoot, templateFiles, dryRun, version, badgeStyle, dependencyValues, skipVersionFooter)
	})
}

func runSchema(_ *cobra.Command, _ []string) error {
	initializeCli()

	chartSearchRoot := viper.GetString("chart-search-root")
	outputRel := viper.GetString("schema-output-file")
	dryRun := viper.GetBool("dry-run")

	cleanOutput, err := validateSchemaOutputPath(outputRel)
	if err != nil {
		return err
	}

	parallelism := runtime.NumCPU() * 2
	if dryRun {
		parallelism = 1
	}

	infoByChartPath, err := readDocumentationInfoByChartPath(chartSearchRoot, parallelism)
	if err != nil {
		return err
	}

	type chartErr struct {
		chart string
		err   error
	}

	eg, ctx := errgroup.WithContext(context.Background())
	eg.SetLimit(parallelism)

	failCh := make(chan chartErr, len(infoByChartPath))
	for chartDir, info := range infoByChartPath {
		chartDir := chartDir
		info := info
		eg.Go(func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			data, err := generateSchema(info)
			if err != nil {
				ce := fmt.Errorf("schema generation failed: %w", err)
				failCh <- chartErr{chartDir, ce}
				log.Warnf("schema generation failed for %s: %v", chartDir, err)
				return ce
			}

			outPath, err := joinChartOutputPath(chartSearchRoot, chartDir, cleanOutput)
			if err != nil {
				failCh <- chartErr{chartDir, err}
				log.Warnf("invalid output path for %s: %v", chartDir, err)
				return err
			}

			if dryRun {
				fmt.Printf("=== %s/%s ===\n%s\n", chartDir, cleanOutput, string(data))
				return nil
			}
			if err := writeSchemaFile(outPath, data); err != nil {
				failCh <- chartErr{chartDir, err}
				log.Warnf("failed writing schema for %s: %v", chartDir, err)
				return err
			}
			return nil
		})
	}

	_ = eg.Wait()
	close(failCh)

	var failed []string
	var details []string
	for f := range failCh {
		failed = append(failed, f.chart)
		details = append(details, fmt.Sprintf("%s: %v", f.chart, f.err))
	}
	if len(failed) > 0 {
		log.Warn(strings.Join(details, "; "))
		return fmt.Errorf("failed generating schema for charts: %s", strings.Join(failed, ", "))
	}
	return nil
}

func helmDocs(_ *cobra.Command, _ []string) {
	initializeCli()

	chartSearchRoot := viper.GetString("chart-search-root")
	dryRun := viper.GetBool("dry-run")

	parallelism := runtime.NumCPU() * 2

	// On dry runs all output goes to stdout, and so as to not jumble things, generate serially.
	if dryRun {
		parallelism = 1
	}

	documentationInfoByChartPath, err := readDocumentationInfoByChartPath(chartSearchRoot, parallelism)
	if err != nil {
		log.Fatal(err)
	}

	writeDocumentation(chartSearchRoot, documentationInfoByChartPath, dryRun, parallelism)
}

func main() {
	command, err := newHelmDocsCommand(helmDocs)
	if err != nil {
		log.Errorf("Failed to create the CLI commander: %s", err)
		os.Exit(1)
	}

	if err := command.Execute(); err != nil {
		log.Errorf("Failed to start the CLI: %s", err)
		os.Exit(1)
	}
}
