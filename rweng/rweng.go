package rweng

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/go-yaml/yaml"
	"go.uber.org/zap"
)

type FilterCfg struct {
	Name     string `yaml:"name"`
	Match    string `yaml:"match"`
	Template string `yaml:"template"`
}

type FilterTemplate struct {
	Name     string
	Match    string
	Template *template.Template
}

// EngCfg defines an engine configuration
type EngCfg struct {
	PostBan []string    `yaml:"postBan"`
	UrlBan  []string    `yaml:"urlBan"`
	Filter  []FilterCfg `yaml:"postFilter"`
}

// Eng http.Request rule engine.
type Eng struct {
	cfg     EngCfg
	postBan []*regexp.Regexp
	urlBan  []*regexp.Regexp
	filter  map[*regexp.Regexp]FilterTemplate
	logger  *zap.Logger
}

// ProcessRequest performs any rules on matching requests
func (e *Eng) ProcessRequest(w http.ResponseWriter, r *http.Request) {

	b, _ := ioutil.ReadAll(r.Body)
	r.Body.Close()

	// run filter if there is a body
	if len(b) > 0 {
		for rgx, filter := range e.filter {

			// find the match first and populate data structure
			matches := rgx.FindAll(bytes.ToLower(b), len(b))
			for _, match := range matches {
				filter.Match = string(match)
				// send the match to the template
				var tplReturn bytes.Buffer
				if err := filter.Template.Execute(&tplReturn, filter); err != nil {
					// something bad happened
					e.logger.Error("Filter failed: " + err.Error())
					continue
				}

				b = rgx.ReplaceAll(b, tplReturn.Bytes())
			}
		}
	}

	// search for qstring contraband
	for _, rgx := range e.urlBan {
		buri := bytes.ToLower([]byte(r.RequestURI))
		if rgx.Match(buri) {
			e.logger.Warn("URL contraband found.", zap.String("Regexp", rgx.String()), zap.ByteString("PostBody", buri))
			r.URL.Path = "/"
			r.URL.RawQuery = ""
			break
		}
	}

	// search for posted contraband
	for _, rgx := range e.postBan {
		if rgx.Match(bytes.ToLower(b)) {
			e.logger.Warn("Posted contraband found.", zap.String("Regexp", rgx.String()), zap.ByteString("PostBody", b))
			b = []byte{}
			break
		}
	}

	body := ioutil.NopCloser(bytes.NewReader(b))

	r.Body = body
	r.ContentLength = int64(len(b))
	r.Header.Set("Content-Length", strconv.Itoa(len(b)))

}

// NewEngFromYml loads an engine from yaml data
func NewEngFromYml(filename string, logger *zap.Logger) (*Eng, error) {

	ymlData, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	engCfg := EngCfg{}

	err = yaml.Unmarshal([]byte(ymlData), &engCfg)
	if err != nil {
		return nil, err
	}

	postBan := make([]*regexp.Regexp, 0)

	for _, r := range engCfg.PostBan {
		rxp := regexp.MustCompile(strings.ToLower(r))
		postBan = append(postBan, rxp)
	}

	urlBan := make([]*regexp.Regexp, 0)

	for _, r := range engCfg.UrlBan {
		rxp := regexp.MustCompile(strings.ToLower(r))
		urlBan = append(urlBan, rxp)
	}

	filter := make(map[*regexp.Regexp]FilterTemplate, 0)

	for _, filterCfg := range engCfg.Filter {
		rxp := regexp.MustCompile(strings.ToLower(filterCfg.Match))
		tmpl, err := template.New(filterCfg.Name).Funcs(sprig.TxtFuncMap()).Parse(filterCfg.Template)
		if err != nil {
			logger.Error("Template parsing error: " + err.Error())
		}

		filter[rxp] = FilterTemplate{
			Name:     filterCfg.Name,
			Template: tmpl,
		}
	}

	eng := &Eng{
		cfg:     engCfg,
		postBan: postBan,
		urlBan:  urlBan,
		filter:  filter,
		logger:  logger,
	}

	return eng, nil
}
