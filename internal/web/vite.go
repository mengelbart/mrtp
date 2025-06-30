package web

import "encoding/json"

type viteTemplateData struct {
	Chunk          chunk
	ImportedChunks []chunk
}

type chunk struct {
	File           string   `json:"file"`
	Name           string   `json:"name"`
	Src            string   `json:"src"`
	IsEntry        bool     `json:"isEntry"`
	IsDynamicEntry bool     `json:"IsDynamicEntry"`
	Imports        []string `json:"imports,omitempty"`
	DynamicImports []string `json:"dynamicImports,omitempty"`
	CSS            []string `json:"css,omitempty"`
}

type manifest map[string]chunk

func (m *manifest) parse(data []byte) error {
	if err := json.Unmarshal(data, m); err != nil {
		return nil
	}
	return nil
}

func (m manifest) importedChunks(name string) []chunk {
	seen := map[string]struct{}{}
	var getImportedChunks func(c chunk) []chunk
	getImportedChunks = func(c chunk) []chunk {
		chunks := []chunk{}
		for _, file := range c.Imports {
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			importee := m[file]
			chunks = append(chunks, getImportedChunks(importee)...)
			chunks = append(chunks, importee)
		}
		return chunks
	}
	return getImportedChunks(m[name])
}
