package controllers

import (
    "bytes"
    "github.com/iktech/pepper/model"
    "net/http"
)

var Debug   bool

type Controller interface {
    Handle(r *http.Request) (int, string, string, *bytes.Buffer, *model.ProcessingError)
}

type Model struct {
   *model.Model
}

func (m Model) Handle(_ *http.Request) (int, string, string, *bytes.Buffer, *model.ProcessingError) {
    return m.Render(Debug, m)
}
