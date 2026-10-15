package tencentdocs

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

const nativeFormatVersion = 2

func nativeItem(file remoteFile) types.FetchedItem {
	return types.FetchedItem{ExternalID: siteSaaS + ":" + file.ID, Title: file.Name, SourceResourceID: file.ID, UpdatedAt: time.UnixMilli(file.ModifiedAt).UTC(), Metadata: map[string]string{
		"source_url": file.URL, "source_type": file.Ext, "source_site": siteSaaS, "channel": "tencent_docs", "tencent_docs_format_version": "2",
	}}
}

func nativeSkip(file remoteFile, code, reason string) types.FetchedItem {
	item := nativeItem(file)
	item.Metadata["skip_reason_code"] = code
	item.Metadata["skip_reason"] = reason
	return item
}

func nativeFailure(file remoteFile, code, reason string) types.FetchedItem {
	item := nativeItem(file)
	item.Metadata["error"] = reason
	item.Metadata["error_reason_code"] = code
	item.Metadata["error_reason"] = reason
	return item
}

func nativeExpectedExt(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "tencentdoc", "smartcanvas":
		return "docx"
	case "tencentsheet", "smartsheet":
		return "xlsx"
	case "tencentslide":
		return "pptx"
	default:
		return strings.ToLower(strings.TrimPrefix(ext, "."))
	}
}

func supportedNativeExt(ext string) bool {
	switch ext {
	case "doc", "docx", "ppt", "pptx", "xls", "xlsx", "csv", "pdf", "png", "jpg", "jpeg", "gif", "webp", "bmp", "txt", "md", "html", "htm", "epub":
		return true
	}
	return false
}

func waitNativeExport(ctx context.Context, client *mcpClient, file remoteFile) (string, string, error) {
	args := map[string]any{"file_id": file.ID, "export_type": 0}
	if file.Ext == "smartcanvas" {
		args["target_ext"] = "docx"
	}
	var task struct {
		TaskID string `json:"task_id"`
	}
	if err := client.call(ctx, "manage.apply_download", args, &task); err != nil {
		return "", "", err
	}
	if task.TaskID == "" {
		return "", "", errors.New("export task missing")
	}
	for attempt := 0; attempt < 40; attempt++ {
		var result struct {
			Progress int    `json:"progress"`
			URL      string `json:"url"`
			Title    string `json:"title"`
		}
		if err := client.call(ctx, "manage.query_task", map[string]any{"task_id": task.TaskID}, &result); err != nil {
			return "", "", err
		}
		if result.Progress == 100 && validSourceURL(result.URL) != "" {
			return result.URL, result.Title, nil
		}
		delay := 3 * time.Second
		if client.pollDelay != nil {
			delay = *client.pollDelay
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", "", errors.New("export timed out")
}

func downloadNative(ctx context.Context, client *mcpClient, address string) ([]byte, string, error) {
	transport := client.downloadHTTP
	if transport == nil {
		transport = datasource.NewConnectorHTTPClient(90 * time.Second)
	}
	limit := utils.GetMaxFileSize()
	for attempt := 0; attempt < 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, "", errors.New("invalid download address")
		}
		// Signed downloads never receive the user's MCP Authorization header.
		response, err := transport.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			if attempt == 2 {
				return nil, "", errors.New("下载请求失败")
			}
			continue
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			if response.StatusCode >= 500 && attempt < 2 {
				continue
			}
			return nil, "", fmt.Errorf("下载接口返回 HTTP %d", response.StatusCode)
		}
		if response.ContentLength > limit {
			response.Body.Close()
			return nil, "", errors.New("文件超过大小限制")
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		response.Body.Close()
		if err != nil {
			return nil, "", errors.New("下载内容不完整")
		}
		if int64(len(body)) > limit {
			return nil, "", errors.New("文件超过大小限制")
		}
		if len(body) == 0 {
			return nil, "", errors.New("下载文件为空")
		}
		fileName := ""
		if _, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition")); err == nil {
			fileName = params["filename"]
		}
		return body, fileName, nil
	}
	return nil, "", errors.New("下载失败")
}

// Inspect package structure rather than trusting an extension or an HTTP success.
func nativePackageInfo(data []byte) (ext string, empty bool, imageOnly bool, err error) {
	if bytes.HasPrefix(data, []byte("PK")) {
		archive, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if e != nil {
			return "", false, false, errors.New("导出文件不是有效的 Office 文件")
		}
		hasText, hasImage := false, false
		for _, file := range archive.File {
			if file.Name == "word/document.xml" {
				ext = "docx"
			}
			if file.Name == "ppt/presentation.xml" {
				ext = "pptx"
			}
			if file.Name == "xl/workbook.xml" {
				ext = "xlsx"
			}
			if strings.Contains(file.Name, "/media/") && !file.FileInfo().IsDir() {
				hasImage = true
			}
			if file.Name == "word/document.xml" || strings.HasPrefix(file.Name, "ppt/slides/slide") && strings.HasSuffix(file.Name, ".xml") {
				if file.UncompressedSize64 > uint64(utils.GetMaxFileSize()) {
					return "", false, false, errors.New("Office 内容超过大小限制")
				}
				stream, e := file.Open()
				if e != nil {
					return "", false, false, e
				}
				decoder := xml.NewDecoder(io.LimitReader(stream, utils.GetMaxFileSize()))
				for {
					token, e := decoder.Token()
					if e == io.EOF {
						break
					}
					if e != nil {
						stream.Close()
						return "", false, false, errors.New("Office XML 无效")
					}
					if element, ok := token.(xml.StartElement); ok && element.Name.Local == "t" {
						var text string
						if decoder.DecodeElement(&text, &element) == nil && strings.TrimSpace(text) != "" {
							hasText = true
						}
					}
				}
				stream.Close()
			}
		}
		if ext == "" {
			return "zip", false, false, nil
		}
		return ext, (ext == "docx" || ext == "pptx") && !hasText && !hasImage, ext == "pptx" && !hasText && hasImage, nil
	}
	if bytes.HasPrefix(data, []byte("%PDF-")) {
		return "pdf", false, false, nil
	}
	if bytes.HasPrefix(data, []byte{0xd0, 0xcf, 0x11, 0xe0}) {
		return "ole", false, false, nil
	}
	detected := http.DetectContentType(data)
	switch {
	case strings.HasPrefix(detected, "image/png"):
		return "png", false, false, nil
	case strings.HasPrefix(detected, "image/jpeg"):
		return "jpg", false, false, nil
	case strings.HasPrefix(detected, "image/gif"):
		return "gif", false, false, nil
	case strings.HasPrefix(detected, "image/webp"):
		return "webp", false, false, nil
	case strings.HasPrefix(detected, "text/"):
		return "text", false, false, nil
	}
	return "", false, false, errors.New("无法识别下载文件格式")
}

func fetchNativeFile(ctx context.Context, client *mcpClient, file remoteFile, folderPath string) types.FetchedItem {
	item := fetchOriginalFile(ctx, client, file, folderPath)
	if ctx.Err() != nil || item.Metadata["error"] == "" && item.Metadata["skip_reason_code"] != "tencent_docs_unsupported_format" {
		return item
	}
	// Online source types can expose text even when export is unavailable.
	// Attachments are not substituted with a potentially incomplete text view.
	switch file.Ext {
	case "tencentdoc", "smartcanvas", "tencentsheet", "tencentslide", "smartsheet", "flowchart", "mind":
	default:
		return item
	}
	var result struct {
		Content string `json:"content"`
	}
	if err := client.call(ctx, "manage.get_content", map[string]any{"file_id": file.ID}, &result); err != nil {
		return item
	}
	content := strings.TrimSpace(result.Content)
	if content == "" || content == "{}" || content == "[]" {
		return item
	}
	fallback := nativeItem(file)
	fallback.FileName = path.Join(folderPath, safeSegment(file.Name)+".md")
	fallback.ContentType = "text/markdown"
	fallback.Content = []byte("# " + file.Name + "\n\n" + content + "\n")
	if int64(len(fallback.Content)) > utils.GetMaxFileSize() {
		return item
	}
	fallback.Metadata["tencent_docs_md_fallback"] = "true"
	return fallback
}

func fetchOriginalFile(ctx context.Context, client *mcpClient, file remoteFile, folderPath string) types.FetchedItem {
	if nativeExpectedExt(file.Ext) == "zip" {
		return nativeSkip(file, "tencent_docs_archive_unsupported", "压缩包暂不支持")
	}
	address, taskName, err := waitNativeExport(ctx, client, file)
	if err != nil {
		code, reason := "tencent_docs_export_failed", "导出失败，请稍后重试"
		if strings.Contains(err.Error(), "code 10090") {
			item := nativeFailure(file, "tencent_docs_export_service_error", "腾讯导出服务返回错误 10090")
			item.Metadata["error_reason_code_value"] = "10090"
			return item
		}
		if strings.Contains(err.Error(), "400003") || strings.Contains(err.Error(), "403") {
			code, reason = "tencent_docs_permission_denied", "没有导出或下载权限"
		}
		return nativeFailure(file, code, reason)
	}
	data, downloadName, err := downloadNative(ctx, client, address)
	if err != nil {
		if strings.Contains(err.Error(), "超过大小限制") {
			item := nativeFailure(file, "tencent_docs_file_too_large", fmt.Sprintf("文件超过当前 %d MB 大小限制", utils.GetMaxFileSizeMB()))
			item.Metadata["error_reason_code_value"] = fmt.Sprint(utils.GetMaxFileSizeMB())
			return item
		}
		return nativeFailure(file, "tencent_docs_download_failed", err.Error())
	}
	actual, empty, imageOnly, err := nativePackageInfo(data)
	if err != nil {
		return nativeFailure(file, "tencent_docs_invalid_file", err.Error())
	}
	expected := nativeExpectedExt(file.Ext)
	if !supportedNativeExt(expected) {
		for _, name := range []string{downloadName, taskName} {
			e := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
			if supportedNativeExt(e) {
				expected = e
				break
			}
		}
		if !supportedNativeExt(expected) {
			expected = actual
		}
	}
	if !supportedNativeExt(expected) {
		return nativeSkip(file, "tencent_docs_unsupported_format", "该导出格式暂不支持解析")
	}
	compatible := actual == expected || actual == "ole" && (expected == "doc" || expected == "xls" || expected == "ppt") || actual == "text" && (expected == "txt" || expected == "md" || expected == "html" || expected == "htm" || expected == "csv") || actual == "jpg" && expected == "jpeg"
	if !compatible {
		return nativeFailure(file, "tencent_docs_invalid_file", "下载格式与文件类型不匹配")
	}
	if empty {
		return nativeSkip(file, "tencent_docs_empty_document", "空文档，已跳过")
	}
	item := nativeItem(file)
	base := safeSegment(file.Name)
	if strings.EqualFold(path.Ext(base), "."+expected) {
		base = strings.TrimSuffix(base, path.Ext(base))
	}
	item.FileName = path.Join(folderPath, base+"."+expected)
	item.Content = data
	item.ContentType = http.DetectContentType(data)
	if expected == "pptx" || expected == "ppt" {
		item.Metadata["tencent_docs_ppt_parser"] = "builtin"
	}
	if imageOnly {
		item.Metadata["tencent_docs_requires_ocr"] = "true"
	}
	// Do not put the temporary signed download URL into FetchedItem.URL or metadata.
	return item
}
