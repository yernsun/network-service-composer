package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/text/language"
)

type Locale string

const (
	English            Locale = "en"
	Japanese           Locale = "ja"
	German             Locale = "de"
	SimplifiedChinese  Locale = "zh-CN"
	TraditionalChinese Locale = "zh-TW"
	French             Locale = "fr"
	Russian            Locale = "ru"
	Spanish            Locale = "es"
)

type Option struct {
	Code       Locale
	NativeName string
}

//go:embed locales.json
var localeDefinition []byte
var options []Option
var localeTags []language.Tag
var matcher language.Matcher

//go:embed catalogs/*.json
var catalogFiles embed.FS

var (
	catalogs      map[Locale]map[string]string
	catalogJSON   map[Locale][]byte
	placeholderRE = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_]*)\}`)
)

type localeContextKey struct{}

func localeOptions(data []byte) []Option {
	var definitions []struct {
		Code Locale `json:"code"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &definitions); err != nil {
		panic(err)
	}
	result := make([]Option, 0, len(definitions))
	for _, d := range definitions {
		result = append(result, Option{Code: d.Code, NativeName: d.Name})
	}
	return result
}

func init() {
	options = localeOptions(localeDefinition)
	for _, option := range options {
		localeTags = append(localeTags, language.MustParse(string(option.Code)))
	}
	matcher = language.NewMatcher(localeTags)
	var err error
	catalogs, catalogJSON, err = loadCatalogs()
	if err != nil {
		panic(err)
	}
}

func Supported() []Option {
	result := make([]Option, len(options))
	copy(result, options)
	return result
}

func SupportedCodes() []string {
	result := make([]string, len(options))
	for index, option := range options {
		result[index] = string(option.Code)
	}
	return result
}

func IsSupportedCode(value string) bool {
	for _, option := range options {
		if value == string(option.Code) {
			return true
		}
	}
	return false
}

func Parse(value string) (Locale, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	tag, err := language.Parse(value)
	if err != nil {
		return "", false
	}
	_, index, confidence := matcher.Match(tag)
	if confidence == language.No || index < 0 || index >= len(options) {
		return "", false
	}
	return options[index].Code, true
}

func Resolve(cookieValue, acceptLanguage, fallback string) Locale {
	if locale, ok := Parse(cookieValue); ok {
		return locale
	}
	defaultLocale, ok := Parse(fallback)
	if !ok {
		defaultLocale = English
	}
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return defaultLocale
	}
	_, index, confidence := matcher.Match(tags...)
	if confidence == language.No || index < 0 || index >= len(options) {
		return defaultLocale
	}
	return options[index].Code
}

func WithLocale(ctx context.Context, locale Locale) context.Context {
	if _, ok := catalogs[locale]; !ok {
		locale = English
	}
	return context.WithValue(ctx, localeContextKey{}, locale)
}

func FromContext(ctx context.Context) Locale {
	if locale, ok := ctx.Value(localeContextKey{}).(Locale); ok {
		if _, exists := catalogs[locale]; exists {
			return locale
		}
	}
	return English
}

func Text(locale Locale, key string, params map[string]any) string {
	translations, ok := catalogs[locale]
	if !ok {
		translations = catalogs[English]
	}
	value, ok := translations[key]
	if !ok {
		value, ok = catalogs[English][key]
	}
	if !ok {
		return key
	}
	return interpolate(value, params)
}

func Has(locale Locale, key string) bool {
	values, ok := catalogs[locale]
	if !ok {
		return false
	}
	_, ok = values[key]
	return ok
}

func CatalogJSON(locale Locale) ([]byte, bool) {
	value, ok := catalogJSON[locale]
	if !ok {
		return nil, false
	}
	result := make([]byte, len(value))
	copy(result, value)
	return result, true
}

func interpolate(value string, params map[string]any) string {
	if len(params) == 0 {
		return value
	}
	return placeholderRE.ReplaceAllStringFunc(value, func(match string) string {
		parts := placeholderRE.FindStringSubmatch(match)
		parameter, ok := params[parts[1]]
		if !ok {
			return match
		}
		return fmt.Sprint(parameter)
	})
}

func loadCatalogs() (map[Locale]map[string]string, map[Locale][]byte, error) {
	loaded := make(map[Locale]map[string]string, len(options))
	encoded := make(map[Locale][]byte, len(options))
	for _, option := range options {
		path := "catalogs/" + string(option.Code) + ".json"
		data, err := catalogFiles.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("read i18n catalog %s: %w", option.Code, err)
		}
		values := make(map[string]string)
		if err = json.Unmarshal(data, &values); err != nil {
			return nil, nil, fmt.Errorf("decode i18n catalog %s: %w", option.Code, err)
		}
		loaded[option.Code] = values
		canonical, marshalErr := json.Marshal(values)
		if marshalErr != nil {
			return nil, nil, fmt.Errorf("encode i18n catalog %s: %w", option.Code, marshalErr)
		}
		encoded[option.Code] = canonical
	}
	if err := validateCatalogs(loaded); err != nil {
		return nil, nil, err
	}
	return loaded, encoded, nil
}

func validateCatalogs(values map[Locale]map[string]string) error {
	english := values[English]
	if len(english) == 0 {
		return fmt.Errorf("English i18n catalog is empty")
	}
	keys := make([]string, 0, len(english))
	for key, value := range english {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			return fmt.Errorf("English i18n catalog contains an empty key or value")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, option := range options {
		translations := values[option.Code]
		if len(translations) != len(english) {
			return fmt.Errorf("i18n catalog %s has %d keys; English has %d", option.Code, len(translations), len(english))
		}
		for _, key := range keys {
			translated, ok := translations[key]
			if !ok || strings.TrimSpace(translated) == "" {
				return fmt.Errorf("i18n catalog %s is missing key %s", option.Code, key)
			}
			if strings.Join(placeholders(translated), ",") != strings.Join(placeholders(english[key]), ",") {
				return fmt.Errorf("i18n catalog %s key %s has different placeholders", option.Code, key)
			}
		}
	}
	return nil
}

func placeholders(value string) []string {
	matches := placeholderRE.FindAllStringSubmatch(value, -1)
	result := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if _, ok := seen[match[1]]; ok {
			continue
		}
		seen[match[1]] = struct{}{}
		result = append(result, match[1])
	}
	sort.Strings(result)
	return result
}
