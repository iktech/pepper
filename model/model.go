package model

import (
	"bytes"
	"html/template"
	"io/fs"
	"log"
	"reflect"
)

type Model struct {
	Path               string
	TemplatesDirectory fs.FS
	Logger             *log.Logger
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

func isset(name string, data interface{}) bool {
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
		m.Logger.Printf("using %s template", m.Template)
	}

	patterns := []string{m.Template}
	patterns = append(patterns, m.Includes...)

	t, err := template.New(m.Template).Funcs(template.FuncMap{"isset": isset}).ParseFS(m.TemplatesDirectory, patterns...)
	if err != nil {
		m.Logger.Println(err)
		return 0, "", "", nil, &ProcessingError{ResponseCode: 500}
	}

	var buf bytes.Buffer

	err = t.Execute(&buf, &data)
	if err != nil {
		m.Logger.Println(err)
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
