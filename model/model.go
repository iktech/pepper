package model

import (
	"bytes"
	"html/template"
	"io/fs"
	"log/slog"
	"reflect"
)

const (
	KeyError       = "error"
	KeyComponent   = "component"
	ComponentModel = "model"
)

type Model struct {
	Path               string
	TemplatesDirectory fs.FS
	Template           string
	Includes           []string
	ResponseCode       int
	ContentType        string
	GoogleAnalyticsId  string
}

type ProcessingError struct {
	ResponseCode int
	Data         interface{}
}

func (m Model) IsActive(path string) string {
	if m.Path == path {
		return "link link-selected"
	}

	return "link"
}

func IsSet(name string, data interface{}) bool {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return false
	}
	return v.FieldByName(name).IsValid()
}

func (m Model) Render(Debug bool, data interface{}) (int, string, string, *bytes.Buffer, *ProcessingError) {
	if Debug {
		slog.Debug("using %s template", m.Template, KeyComponent, ComponentModel)
	}

	patterns := []string{m.Template}
	patterns = append(patterns, m.Includes...)

	t, err := template.New(m.Template).Funcs(template.FuncMap{"isset": IsSet}).ParseFS(m.TemplatesDirectory, patterns...)
	if err != nil {
		slog.Error("cannot create template", KeyError, err, KeyComponent, ComponentModel)
		return 0, "", "", nil, &ProcessingError{ResponseCode: 500}
	}

	var buf bytes.Buffer

	err = t.Execute(&buf, &data)
	if err != nil {
		slog.Error("cannot render document from template", KeyError, err, KeyComponent, ComponentModel)
		return 0, "", "", nil, &ProcessingError{ResponseCode: 500}
	}

	code := m.ResponseCode
	if code == 0 {
		code = 200
	}

	contentType := m.ContentType
	if m.ContentType == "" {
		contentType = "text/html"
	}

	return code, "", contentType, &buf, nil
}
