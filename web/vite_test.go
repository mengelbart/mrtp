package web

import (
	"bytes"
	"fmt"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
)

func TestLoadViteManifest(t *testing.T) {
	data := `{
  "_shared-B7PI925R.js": {
    "file": "assets/shared-B7PI925R.js",
    "name": "shared",
    "css": ["assets/shared-ChJ_j-JJ.css"]
  },
  "_shared-ChJ_j-JJ.css": {
    "file": "assets/shared-ChJ_j-JJ.css",
    "src": "_shared-ChJ_j-JJ.css"
  },
  "baz.js": {
    "file": "assets/baz-B2H3sXNv.js",
    "name": "baz",
    "src": "baz.js",
    "isDynamicEntry": true
  },
  "views/bar.js": {
    "file": "assets/bar-gkvgaI9m.js",
    "name": "bar",
    "src": "views/bar.js",
    "isEntry": true,
    "imports": ["_shared-B7PI925R.js"],
    "dynamicImports": ["baz.js"]
  },
  "views/foo.js": {
    "file": "assets/foo-BRBmoGS9.js",
    "name": "foo",
    "src": "views/foo.js",
    "isEntry": true,
    "imports": ["_shared-B7PI925R.js"],
    "css": ["assets/foo-5UjPuW-k.css"]
  }
}`

	var m manifest
	err := m.parse([]byte(data))
	assert.NoError(t, err)

	expected := manifest{
		"_shared-B7PI925R.js": {
			File: "assets/shared-B7PI925R.js",
			Name: "shared",
			CSS:  []string{"assets/shared-ChJ_j-JJ.css"},
		},
		"_shared-ChJ_j-JJ.css": {
			File: "assets/shared-ChJ_j-JJ.css",
			Src:  "_shared-ChJ_j-JJ.css",
		},
		"baz.js": {
			File:           "assets/baz-B2H3sXNv.js",
			Name:           "baz",
			Src:            "baz.js",
			IsDynamicEntry: true,
		},
		"views/foo.js": {
			File:    "assets/foo-BRBmoGS9.js",
			Name:    "foo",
			Src:     "views/foo.js",
			IsEntry: true,
			Imports: []string{"_shared-B7PI925R.js"},
			CSS:     []string{"assets/foo-5UjPuW-k.css"},
		},
		"views/bar.js": {
			File:           "assets/bar-gkvgaI9m.js",
			Name:           "bar",
			Src:            "views/bar.js",
			IsEntry:        true,
			Imports:        []string{"_shared-B7PI925R.js"},
			DynamicImports: []string{"baz.js"},
		},
	}

	assert.Equal(t, expected, m)

	tmpl := `{{ range .Chunk.CSS }}<link rel="stylesheet" href="/{{ . }}" />{{ end }}
{{ range .ImportedChunks }}{{ range .CSS }}<link rel="stylesheet" href="/{{ . }}" />{{ end }}{{ end }}
<script type="module" src="/{{ .Chunk.File }}"></script>
{{ range .ImportedChunks }}<link rel="modulepreload" href="/{{ .File }}" />{{ end }}
`

	tmplData := viteTemplateData{
		Chunk:          m["views/foo.js"],
		ImportedChunks: m.importedChunks("views/bar.js"),
	}

	t1 := template.Must(template.New("test").Parse(tmpl))

	buffer := bytes.NewBuffer(nil)
	err = t1.Execute(buffer, tmplData)
	assert.NoError(t, err)

	fmt.Printf("result: '%s'\n", buffer.String())

	buffer.Reset()
	tmplData = viteTemplateData{
		Chunk:          m["views/bar.js"],
		ImportedChunks: m.importedChunks("views/bar.js"),
	}
	err = t1.Execute(buffer, tmplData)
	assert.NoError(t, err)

	fmt.Printf("result: '%s'\n", buffer.String())

}
