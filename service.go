package pepper

import (
	"bytes"
	"embed"
	"fmt"
	"github.com/iktech/pepper/controllers"
	"github.com/iktech/pepper/model"
	"github.com/opentracing/opentracing-go"
	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/spf13/viper"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed errorPages
var errorPageFiles embed.FS

type Redirect struct {
	Location string
	Code     uint
}

type ErrorPageDefinition struct {
	Name       string
	IsDefault  bool
	IsTemplate bool
	Data       interface{}
}

type Service struct {
	staticHandler http.Handler
	templates     fs.FS
	routerMap     map[string]controllers.Controller
	redirects     map[string]Redirect
}

var (
	Logger           *log.Logger
	Debug            bool
	Port             int
	staticFiles      embed.FS
	templates        fs.FS
	ErrorPages       map[int]*ErrorPageDefinition
	Tracer           opentracing.Tracer
	GoogleAnayticsId string
)

func CreateService(sf embed.FS, t embed.FS, customize func(map[string]controllers.Controller) map[string]controllers.Controller) {
	if Logger == nil {
		Logger = log.New(os.Stdout, "pepper: ", log.LstdFlags)
	}

	staticFiles = sf
	templates = t

	viper.SetEnvPrefix("http")
	viper.AllowEmptyEnv(true)

	viper.SetDefault("http.content.useEmbedded", true)
	viper.SetDefault("http.content.templatesDirectory", "templates")
	viper.SetDefault("http.content.staticDirectory", "static")
	viper.SetDefault("http.port", 8888)
	viper.SetDefault("http.context", "/")

	_ = viper.BindEnv("http.content.useEmbedded", "HTTP_USE_EMBEDDED")
	_ = viper.BindEnv("google.analytics.id", "GOOGLE_ANALYTICS_ID")

	controllers.Debug = Debug
	useEmbedded := viper.GetBool("http.content.useEmbedded")
	GoogleAnayticsId = viper.GetString("google.analytics.id")
	// Launch web server on port 80
	ErrorPages = make(map[int]*ErrorPageDefinition)
	ErrorPages[400] = &ErrorPageDefinition{
		Name:      "400.html",
		IsDefault: true,
	}

	ErrorPages[401] = &ErrorPageDefinition{
		Name:      "401.html",
		IsDefault: true,
	}

	ErrorPages[403] = &ErrorPageDefinition{
		Name:      "403.html",
		IsDefault: true,
	}

	ErrorPages[404] = &ErrorPageDefinition{
		Name:      "404.html",
		IsDefault: true,
	}

	ErrorPages[405] = &ErrorPageDefinition{
		Name:      "405.html",
		IsDefault: true,
	}

	ErrorPages[500] = &ErrorPageDefinition{
		Name:      "500.html",
		IsDefault: true,
	}

	nextRequestID := func() string {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	http.Handle(viper.GetString("http.context"), Tracing(nextRequestID)(Logging(Logger)(requestHandler(useEmbedded, customize))))
	Port = viper.GetInt("http.port")
}

func Run() {
	if err := http.ListenAndServe(":"+strconv.Itoa(Port), nil); err != nil && err != http.ErrServerClosed {
		Logger.Fatalf("Cannot start server: %v", err)
	}
}

func requestHandler(useEmbedded bool, customise func(map[string]controllers.Controller) map[string]controllers.Controller) http.Handler {
	var staticHandler http.Handler

	includes := viper.GetStringSlice("http.includes")

	routerMap := make(map[string]controllers.Controller)
	controls := viper.GetStringMapString("http.controllers")
	var fsRoot fs.FS
	if useEmbedded {
		Logger.Println("using embedded templates")
		fsRoot, _ = fs.Sub(templates, viper.GetString("http.content.templatesDirectory"))
	} else {
		Logger.Println("using templates from the file system")
		templates = os.DirFS(viper.GetString("http.content.templatesDirectory"))
		fsRoot = templates
		//fsRoot, _ = fs.Sub(templates, viper.GetString("http.content.templatesDirectory"))
	}
	for key, value := range controls {
		routerMap[key] = controllers.Model{
			Model: &model.Model{
				Path:               key,
				Template:           value,
				TemplatesDirectory: fsRoot,
				Logger:             Logger,
				Includes:           includes,
				GoogleAnalyticsId:  GoogleAnayticsId,
			},
		}
	}

	routerMap = customise(routerMap)
	fsRoot, _ = fs.Sub(staticFiles, viper.GetString("http.content.staticDirectory"))
	var static = http.FS(fsRoot)

	if useEmbedded {
		Logger.Println("using embedded content")
		staticHandler = http.FileServer(static)

	} else {
		Logger.Println("using content from the file system")
		staticHandler = http.FileServer(http.Dir(viper.GetString("http.content.staticDirectory")))
	}

	redirectsMap := viper.GetStringMap("http.redirects")
	redirects := make(map[string]Redirect)
	for key, value := range redirectsMap {
		v := value.(map[string]interface{})

		code := uint(301)
		if v["code"] != nil {
			code = uint(v["code"].(int))
		}

		location := v["location"].(string)
		if strings.HasPrefix(location, "env.") {
			location = os.Getenv(strings.TrimPrefix(location, "env."))
		}
		redirect := Redirect{
			Code:     code,
			Location: location,
		}

		redirects[key] = redirect
	}

	errorPagesMap := viper.GetStringMap("http.errorPages")
	for key, value := range errorPagesMap {
		var err error
		name := value.(string)
		code := 404
		code, err = strconv.Atoi(key)
		if err != nil {
			Logger.Fatalf("unexpected error code %s in error pages definition", key)
		}

		m := ErrorPages[code]
		if m != nil {
			m.IsDefault = false
			m.Name = name
			m.IsTemplate = strings.HasSuffix(name, ".gohtml")
		} else {
			ErrorPages[code] = &ErrorPageDefinition{
				Name:       name,
				IsDefault:  false,
				IsTemplate: strings.HasSuffix(name, ".gohtml"),
			}
		}
	}

	return Service{staticHandler, templates, routerMap, redirects}
}

func (s Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	span := Tracer.StartSpan("serve-http")

	defer span.Finish()

	path := strings.TrimPrefix(r.URL.Path, "/")
	span.SetTag("resource", path)
	redirect, found := s.redirects[path]
	if found {
		span.LogFields(otlog.String("event", "redirect"), otlog.String("location", redirect.Location))
		w.Header().Set("Location", redirect.Location)
		w.WriteHeader(int(redirect.Code))
		return
	}

	route := s.routerMap[path]
	if route == nil {
		span.LogFields(otlog.String("event", "static-file"))
		s.staticHandler.ServeHTTP(w, r)
	} else {
		span.LogFields(otlog.String("event", "handler"))
		code, redirectUrl, contentType, b, controllerError := route.Handle(r)
		if controllerError != nil {
			message := fmt.Sprintf("cannot handle request %s: %v", path, controllerError)
			span.LogFields(otlog.String("event", "controller-error"), otlog.String("message", message))
			Logger.Println(message)
			b, err := GetErrorPageContent(*controllerError)
			if err != nil {
				Logger.Printf("cannot read error page content: %v", err)
			}

			w.WriteHeader(controllerError.ResponseCode)
			w.Header().Set("Content-Type", "text/html")
			_, err = w.Write(b)
			if err != nil {
				Logger.Printf("cannot write response body: %v", err)
			}
			return
		}

		if code == 301 ||
			code == 302 ||
			code == 303 ||
			code == 307 ||
			code == 308 {
			if redirectUrl != "" {
				w.Header().Set("Location", redirectUrl)
			} else {
				w.Header().Set("Location", r.URL.String())
			}
			w.WriteHeader(code)
			return
		}

		span.LogFields(otlog.String("event", "response"), otlog.Int("code", code), otlog.String("content-type", contentType))
		w.WriteHeader(code)
		w.Header().Set("Content-Type", contentType)
		_, err := w.Write(b.Bytes())
		if err != nil {
			Logger.Println(controllerError)
		}
	}
}

func GetErrorPageContent(pe model.ProcessingError) ([]byte, error) {
	errorDefinition := ErrorPages[pe.ResponseCode]
	if errorDefinition != nil {
		if errorDefinition.IsTemplate {
			fsRoot, _ := fs.Sub(templates, viper.GetString("http.content.templatesDirectory"))

			t, err := template.New(errorDefinition.Name).ParseFS(fsRoot, errorDefinition.Name)
			if err != nil {
				Logger.Printf("cannot create template %s: %v", errorDefinition.Name, err)
				return nil, err
			}
			var buf bytes.Buffer
			err = t.Execute(&buf, &pe.Data)
			if err != nil {
				Logger.Printf("cannot render template %s: %v", errorDefinition.Name, err)
				return nil, err
			}

			return buf.Bytes(), nil
		} else {
			if errorDefinition.IsDefault {
				return errorPageFiles.ReadFile("errorPages/" + errorDefinition.Name)
			} else {
				return staticFiles.ReadFile(errorDefinition.Name)
			}
		}
	}
	return nil, nil
}
