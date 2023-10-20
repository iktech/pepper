package pepper

import (
	"bytes"
	"embed"
	"fmt"
	"github.com/iktech/pepper/authentication"
	"github.com/iktech/pepper/controllers"
	"github.com/iktech/pepper/model"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

//go:embed errorPages
var errorPageFiles embed.FS

const (
	KeyError           = "error"
	KeyComponent       = "component"
	ComponentService   = "service"
	ComponentAccessLog = "access_log"
)

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
	Debug                bool
	Port                 int
	staticFiles          embed.FS
	templates            fs.FS
	ErrorPages           map[int]*ErrorPageDefinition
	GoogleAnayticsId     string
	RequestDurationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "http_router_request_duration",
			Help: "Duration of the HTTP request",
		},
		[]string{"code", "method", "path"},
	)
	RequestDurationSummary = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "http_router_request",
			Help:       "Summary of the HTTP request duration",
			Objectives: map[float64]float64{},
		},
		[]string{"code", "method", "path"},
	)
	Server *http.Server
)

func CreateService(sf embed.FS, t embed.FS, customize func(map[string]controllers.Controller) map[string]controllers.Controller) {
	staticFiles = sf
	templates = t

	viper.SetEnvPrefix("http")
	viper.AllowEmptyEnv(true)

	viper.SetDefault("http.content.useEmbedded", true)
	viper.SetDefault("http.content.templatesDirectory", "templates")
	viper.SetDefault("http.content.staticDirectory", "static")
	viper.SetDefault("http.port", 8888)
	viper.SetDefault("http.context", "/")
	viper.SetDefault("http.password.file", "/etc/pepper/.passwd")

	_ = viper.BindEnv("http.content.useEmbedded", "HTTP_USE_EMBEDDED")
	_ = viper.BindEnv("http.password.file", "HTTP_PASSWORD_FILE")
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

	prometheus.MustRegister(RequestDurationGauge)
	prometheus.MustRegister(RequestDurationSummary)
	nextRequestID := func() string {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	var ba = &authentication.BasicAuthHandler{}
	var prometheusHandler = ba.BasicAuth(viper.GetString("http.password.file"))(promhttp.Handler())

	http.Handle("/metrics", prometheusHandler)
	http.Handle(viper.GetString("http.context"), Tracing(nextRequestID)(Logging()(requestHandler(useEmbedded, customize))))
	Port = viper.GetInt("http.port")
	Server = &http.Server{
		Addr: ":" + strconv.Itoa(Port),
	}
}

func Run() {
	if err := Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("cannot start server", KeyError, err, KeyComponent, ComponentService)
		os.Exit(1)
	}
}

func requestHandler(useEmbedded bool, customise func(map[string]controllers.Controller) map[string]controllers.Controller) http.Handler {
	var staticHandler http.Handler

	includes := viper.GetStringSlice("http.includes")

	routerMap := make(map[string]controllers.Controller)
	controls := viper.GetStringMapString("http.controllers")
	var fsRoot fs.FS
	if useEmbedded {
		slog.Info("using embedded templates", KeyComponent, ComponentService)
		fsRoot, _ = fs.Sub(templates, viper.GetString("http.content.templatesDirectory"))
	} else {
		slog.Info("using templates from the file system", KeyComponent, ComponentService)
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
				Includes:           includes,
				GoogleAnalyticsId:  GoogleAnayticsId,
			},
		}
	}

	routerMap = customise(routerMap)
	fsRoot, _ = fs.Sub(staticFiles, viper.GetString("http.content.staticDirectory"))
	var static = http.FS(fsRoot)

	if useEmbedded {
		slog.Info("using embedded content", KeyComponent, ComponentService)
		staticHandler = http.FileServer(static)

	} else {
		slog.Info("using content from the file system", KeyComponent, ComponentService)
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
			slog.Error("unexpected error code %s in error pages definition", key, KeyComponent, ComponentService)
			os.Exit(1)
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
	ctx := r.Context()
	tracer := otel.Tracer("http-server")
	ctx, span := tracer.Start(r.Context(), "/beta")
	span.End()

	r = r.WithContext(ctx)

	path := strings.TrimPrefix(r.URL.Path, "/")
	span.SetAttributes(attribute.String("resource", path))
	redirect, found := s.redirects[path]
	if found {
		span.SetAttributes(attribute.String("event", "redirect"), attribute.String("location", redirect.Location))
		w.Header().Set("Location", redirect.Location)
		w.WriteHeader(int(redirect.Code))
		return
	}

	route := s.routerMap[path]
	if route == nil {
		span.SetAttributes(attribute.String("event", "static-file"))
		if staticFileExists(path) {
			s.staticHandler.ServeHTTP(w, r)
		} else {
			message := fmt.Sprintf("static file %s does not exist", path)
			span.SetAttributes(attribute.String("event", "controller-error"), attribute.String("message", message))
			slog.Info(message, KeyComponent, ComponentService)
			b, err := GetErrorPageContent(model.ProcessingError{ResponseCode: 404})
			if err != nil {
				slog.Error("cannot read error page content", KeyError, err, KeyComponent, ComponentService)
			}

			w.WriteHeader(404)
			w.Header().Set("Content-Type", "text/html")
			_, err = w.Write(b)
			if err != nil {
				slog.Error("cannot write response body", KeyError, err, KeyComponent, ComponentService)
			}
			return
		}
	} else {
		span.SetAttributes(attribute.String("event", "handler"))
		code, redirectUrl, contentType, b, controllerError := route.Handle(r)
		if controllerError != nil {
			message := fmt.Sprintf("cannot handle request %s: %v", path, controllerError)
			span.SetAttributes(attribute.String("event", "controller-error"), attribute.String("message", message))
			slog.Info(message, KeyComponent, ComponentService)
			w.WriteHeader(controllerError.ResponseCode)

			if contentType == "" {
				contentType = "text/html"
			}

			w.Header().Set("Content-Type", contentType)

			var (
				errorPageContent []byte
				err              error
			)

			if b == nil {
				errorPageContent, err = GetErrorPageContent(*controllerError)
				if err != nil {
					slog.Error("cannot read error page content", KeyError, err, KeyComponent, ComponentService)
				}
			} else {
				errorPageContent = b.Bytes()
			}

			_, err = w.Write(errorPageContent)
			if err != nil {
				slog.Error("cannot write response body", KeyError, err, KeyComponent, ComponentService)
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

		span.SetAttributes(attribute.String("event", "response"), attribute.Int("code", code), attribute.String("content-type", contentType))
		w.WriteHeader(code)
		w.Header().Set("Content-Type", contentType)
		_, err := w.Write(b.Bytes())
		if err != nil {
			slog.Error("controller error", KeyError, controllerError, KeyComponent, ComponentService)
		}
	}
}

func GetErrorPageContent(pe model.ProcessingError) ([]byte, error) {
	errorDefinition := ErrorPages[pe.ResponseCode]
	if errorDefinition != nil {
		if errorDefinition.IsTemplate {
			var fsRoot fs.FS
			if viper.GetBool("http.content.useEmbedded") {
				fsRoot, _ = fs.Sub(templates, viper.GetString("http.content.templatesDirectory"))
			} else {
				fsRoot = templates
			}
			patterns := []string{errorDefinition.Name}
			patterns = append(patterns, viper.GetStringSlice("http.includes")...)

			t, err := template.New(errorDefinition.Name).Funcs(template.FuncMap{"isset": model.IsSet}).ParseFS(fsRoot, patterns...)
			if err != nil {
				slog.Error(fmt.Sprintf("cannot create template %s", errorDefinition.Name), KeyError, err, KeyComponent, ComponentService)
				return nil, err
			}
			var buf bytes.Buffer
			err = t.Execute(&buf, &pe.Data)
			if err != nil {
				slog.Error(fmt.Sprintf("cannot render template %s", errorDefinition.Name), KeyError, err, KeyComponent, ComponentService)
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

func staticFileExists(fileName string) bool {
	useEmbedded := viper.GetBool("http.content.useEmbedded")
	var fsRoot fs.FS
	fsRoot, _ = fs.Sub(staticFiles, viper.GetString("http.content.staticDirectory"))

	if !useEmbedded {
		fsRoot = os.DirFS(viper.GetString("http.content.staticDirectory"))
	}

	var static = http.FS(fsRoot)
	f, err := static.Open(fileName)
	if err == nil {
		_ = f.Close()
		return true
	}
	return false
}
