package tencentdocs

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
)

func testDocx(empty bool) []byte {
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	file, _ := archive.Create("[Content_Types].xml")
	file.Write([]byte(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`))
	file, _ = archive.Create("word/document.xml")
	body := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`
	if !empty {
		body += `<w:p><w:r><w:t>可检索正文</w:t></w:r></w:p>`
	}
	body += `</w:body></w:document>`
	file.Write([]byte(body))
	archive.Close()
	return out.Bytes()
}
func serveNativeDownload(w http.ResponseWriter, r *http.Request, empty bool) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.Header.Get("Authorization") != "" {
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	w.Write(testDocx(empty))
	return true
}
func serveNativeExport(w http.ResponseWriter, r *http.Request, name string) bool {
	var result any
	switch name {
	case "manage.apply_download":
		result = map[string]any{"task_id": "download"}
	case "manage.query_task":
		result = map[string]any{"progress": 100, "url": "http://" + r.Host + "/download"}
	default:
		return false
	}
	json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"structuredContent": result}})
	return true
}
