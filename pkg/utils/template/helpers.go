package template

import (
	"fmt"
	"html/template"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"github.com/iancoleman/strcase"
)

// NamifyWithoutParams will convert a sentence to a golang conventional type name.
// and will remove all parameters that can appear between '{' and '}'.
func NamifyWithoutParams(sentence string) string {
	// Remove parameters
	re := regexp.MustCompile("{[^()]*}")
	sentence = string(re.ReplaceAll([]byte(sentence), []byte("_")))

	return Namify(sentence)
}

// Namify will convert a sentence to a golang conventional type name.
func Namify(sentence string) string {
	// Check if empty
	if len(sentence) == 0 {
		return sentence
	}

	// Upper letters that are preceded with an underscore
	previous := '_'
	for i, r := range sentence {
		if !unicode.IsLetter(previous) && !unicode.IsDigit(previous) {
			sentence = sentence[:i] + strings.ToUpper(string(r)) + sentence[i+1:]
		}
		previous = r
	}

	// Remove everything except alphanumerics
	re := regexp.MustCompile("[^a-zA-Z0-9]")
	sentence = string(re.ReplaceAll([]byte(sentence), []byte("")))

	// Remove leading numbers
	re = regexp.MustCompile("^[0-9]+")
	sentence = string(re.ReplaceAll([]byte(sentence), []byte("")))

	// Upper first letter
	sentence = strings.ToUpper(sentence[:1]) + sentence[1:]

	return sentence
}

type convertKeyFn func(string) string

var convertKeyFuncs = map[string]convertKeyFn{
	"snake": strcase.ToSnake,
	"kebab": strcase.ToKebab,
	"camel": strcase.ToCamel,
	"none":  func(s string) string { return s },
}

var convertKey = convertKeyFuncs["none"]

// ConvertKey is used in template to convert schema property key name
// according to chosen strategy.
func ConvertKey(sentence string) string {
	return convertKey(sentence)
}

// SetConvertKeyFn sets the function used to convert schema property key names.
func SetConvertKeyFn(name string) error {
	fn, ok := convertKeyFuncs[name]
	if !ok {
		return fmt.Errorf("unknown convert key function %s, supported values: snake, kebab, camel, none", name)
	}

	convertKey = fn

	return nil
}

// HasField will check if a struct has a field with the given name.
func HasField(v any, name string) bool {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return false
	}
	return rv.FieldByName(name).IsValid()
}

// DescribeStruct will describe a struct in a human readable way using `%+v`
// format from the standard library.
func DescribeStruct(st any) string {
	return fmt.Sprintf("%+v", st)
}

// MultiLineComment will prefix each line of a comment with "// " in order to
// make it a valid multiline golang comment.
func MultiLineComment(comment string) string {
	comment = strings.TrimSuffix(comment, "\n")
	return strings.ReplaceAll(comment, "\n", "\n// ")
}

// Args is a function used to pass arguments to templates.
func Args(vs ...any) []any {
	return vs
}

// CutSuffix is a function used to remove a suffix to a string.
func CutSuffix(s, suffix string) string {
	s, _ = strings.CutSuffix(s, suffix)
	return s
}

// HelpersFunctions returns the functions that can be used as helpers
// in a golang template.
func HelpersFunctions() template.FuncMap {
	return template.FuncMap{
		"namifyWithoutParam": NamifyWithoutParams,
		"namify":             Namify,
		"convertKey":         ConvertKey,
		"snakeCase":          strcase.ToSnake,
		"hasField":           HasField,
		"describeStruct":     DescribeStruct,
		"multiLineComment":   MultiLineComment,
		"cutSuffix":          CutSuffix,
		"args":               Args,
	}
}
