package email

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	textTemplate "text/template"
)

//go:embed templates/*.html templates/*.txt
var templateFS embed.FS

// Sentinel errors returned by Render.
var (
	// ErrRendererNotInitialized is returned when Render is called on a nil or
	// partially initialized EmbeddedRenderer.
	ErrRendererNotInitialized = errors.New("embedded renderer is not initialized")

	// ErrEmptyTemplateName is returned when Render is called with an empty name.
	ErrEmptyTemplateName = errors.New("template name cannot be empty")

	// ErrInvalidDunningSeverity is returned when DunningNoticeData.Severity is
	// not 1, 2, or 3.
	ErrInvalidDunningSeverity = errors.New("DunningNoticeData.Severity must be 1, 2, or 3")
)

// TemplateRenderer renders named email templates with provided data.
type TemplateRenderer interface {
	Render(name string, data any) (html string, text string, err error)
}

// EmbeddedRenderer renders email templates embedded at compile time via embed.FS.
type EmbeddedRenderer struct {
	htmlTemplates *template.Template
	textTemplates *textTemplate.Template
}

// NewEmbeddedRenderer creates an EmbeddedRenderer by parsing all embedded templates.
func NewEmbeddedRenderer() (*EmbeddedRenderer, error) {
	htmlTmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing HTML templates: %w", err)
	}

	textTmpl, err := textTemplate.ParseFS(templateFS, "templates/*.txt")
	if err != nil {
		return nil, fmt.Errorf("parsing text templates: %w", err)
	}

	return &EmbeddedRenderer{
		htmlTemplates: htmlTmpl,
		textTemplates: textTmpl,
	}, nil
}

// Render executes the named template (name.html and name.txt) with data,
// returning both the HTML and plain-text outputs.
func (r *EmbeddedRenderer) Render(name string, data any) (string, string, error) {
	if r == nil || r.htmlTemplates == nil || r.textTemplates == nil {
		return "", "", ErrRendererNotInitialized
	}
	if name == "" {
		return "", "", ErrEmptyTemplateName
	}

	// Validate typed data structs that have additional constraints.
	if d, ok := data.(DunningNoticeData); ok {
		if d.Severity < 1 || d.Severity > 3 {
			return "", "", fmt.Errorf("%w (got %d)", ErrInvalidDunningSeverity, d.Severity)
		}
	}

	htmlName := name + ".html"
	textName := name + ".txt"

	var htmlBuf bytes.Buffer
	if err := r.htmlTemplates.ExecuteTemplate(&htmlBuf, htmlName, data); err != nil {
		return "", "", fmt.Errorf("rendering HTML template %q: %w", htmlName, err)
	}

	var textBuf bytes.Buffer
	if err := r.textTemplates.ExecuteTemplate(&textBuf, textName, data); err != nil {
		return "", "", fmt.Errorf("rendering text template %q: %w", textName, err)
	}

	return htmlBuf.String(), textBuf.String(), nil
}
