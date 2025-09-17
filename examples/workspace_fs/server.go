package workspace_fs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ggoodman/mcp-server-go/mcp"
	"github.com/ggoodman/mcp-server-go/mcpservice"
	"github.com/ggoodman/mcp-server-go/sessions"
)

// New constructs a local MCP server that exposes a configurable slice of the host
// filesystem as Resources and provides a set of tools for reading and modifying
// files within that sandbox. Tools return resource references where relevant.
//
// Security: access is confined to the provided root directory. All tool paths
// are validated to remain within the root (symlinks are resolved for reads via
// FSResources; tool writes also enforce containment).
func New(root string) mcpservice.ServerCapabilities {
	const baseURI = "fs://workspace"

	// Resources backed by the OS directory
	fsCap := mcpservice.NewFSResources(
		mcpservice.WithOSDir(root),
		mcpservice.WithBaseURI(baseURI),
		mcpservice.WithFSPageSize(200),
		// fsnotify will power listChanged/updated when possible
	)

	// Helper to convert a relative path to a resource URI (percent-escape segments)
	relToURI := func(rel string) string {
		segs := strings.Split(filepath.ToSlash(rel), "/")
		for i, s := range segs {
			segs[i] = url.PathEscape(s)
		}
		return baseURI + "/" + strings.Join(segs, "/")
	}

	// Helper to turn a resource URI back into a relative path in the sandbox
	uriToRel := func(u string) (string, bool) {
		prefix := strings.TrimRight(baseURI, "/") + "/"
		if !strings.HasPrefix(u, prefix) {
			return "", false
		}
		p := strings.TrimPrefix(u, prefix)
		segs := strings.Split(p, "/")
		for i, s := range segs {
			dec, err := url.PathUnescape(s)
			if err != nil {
				return "", false
			}
			segs[i] = dec
		}
		rel := path.Clean(strings.Join(segs, "/"))
		if rel == "." || strings.HasPrefix(rel, "../") {
			return "", false
		}
		return rel, true
	}

	// Enforce that the absolute path remains inside root
	ensureInsideRoot := func(abs string) error {
		real := abs
		if a, err := filepath.Abs(abs); err == nil {
			real = a
		}
		r, err := filepath.Abs(root)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(r, real)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || strings.HasPrefix(rel, "../") {
			return errors.New("path escapes sandbox root")
		}
		return nil
	}

	// Helpers to build content blocks
	resourceLink := func(uri, name, mime string) mcp.ContentBlock {
		return mcp.ContentBlock{Type: "resource_link", URI: uri, Name: name, MimeType: mime}
	}
	embeddedText := func(uri, mime, text string) mcp.ContentBlock {
		return mcp.ContentBlock{Type: "resource", Resource: &mcp.ResourceContents{URI: uri, MimeType: mime, Text: text}}
	}

	// Tool: fs.read — read a file by uri or path and return embedded contents
	type ReadArgs struct {
		URI  string `json:"uri,omitempty"`
		Path string `json:"path,omitempty"`
	}
	readTool := mcpservice.NewTool[ReadArgs]("fs.read", func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[ReadArgs]) error {
		a := r.Args()
		var rel string
		switch {
		case a.URI != "":
			r, ok := uriToRel(a.URI)
			if !ok {
				w.SetError(true)
				_ = w.AppendText(fmt.Sprintf("invalid uri: %s", a.URI))
				return nil
			}
			rel = r
		case a.Path != "":
			rel = path.Clean(strings.TrimPrefix(filepath.ToSlash(a.Path), "/"))
			if rel == "." || strings.HasPrefix(rel, "../") {
				w.SetError(true)
				_ = w.AppendText(fmt.Sprintf("invalid path: %s", a.Path))
				return nil
			}
		default:
			w.SetError(true)
			_ = w.AppendText("must provide either uri or path")
			return nil
		}

		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := ensureInsideRoot(abs); err != nil {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("access denied: %v", err))
			return nil
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				w.SetError(true)
				_ = w.AppendText(fmt.Sprintf("not found: %s", rel))
				return nil
			}
			return err
		}
		uri := relToURI(rel)
		mime := mimeByExt(abs)
		_ = w.AppendBlocks(embeddedText(uri, mime, string(data)))
		return nil
	},
		mcpservice.WithToolDescription("Read a file by URI or path and return its contents as an embedded resource."),
	)

	// Tool: fs.write — create/overwrite a file at path with content
	type WriteArgs struct {
		Path       string `json:"path"`
		Content    string `json:"content"`
		CreateDirs bool   `json:"createDirs,omitempty"`
		Overwrite  bool   `json:"overwrite,omitempty"`
	}
	writeTool := mcpservice.NewTool[WriteArgs]("fs.write", func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[WriteArgs]) error {
		a := r.Args()
		if a.Path == "" {
			w.SetError(true)
			_ = w.AppendText("path is required")
			return nil
		}
		rel := path.Clean(strings.TrimPrefix(filepath.ToSlash(a.Path), "/"))
		if rel == "." || strings.HasPrefix(rel, "../") {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("invalid path: %s", a.Path))
			return nil
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := ensureInsideRoot(abs); err != nil {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("access denied: %v", err))
			return nil
		}
		if a.CreateDirs {
			if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
				return err
			}
		}
		if !a.Overwrite {
			if _, err := os.Stat(abs); err == nil {
				w.SetError(true)
				_ = w.AppendText(fmt.Sprintf("file exists: %s", rel))
				return nil
			}
		}
		if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
			return err
		}
		uri := relToURI(rel)
		mime := mimeByExt(abs)
		_ = w.AppendBlocks(
			resourceLink(uri, path.Base(rel), mime),
			embeddedText(uri, mime, a.Content),
		)
		return nil
	},
		mcpservice.WithToolDescription("Write text to a file at the given path (relative to the workspace root). Returns a resource link and the embedded contents."),
	)

	// Tool: fs.append — append content to a file
	type AppendArgs struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	appendTool := mcpservice.NewTool[AppendArgs]("fs.append", func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[AppendArgs]) error {
		a := r.Args()
		if a.Path == "" {
			w.SetError(true)
			_ = w.AppendText("path is required")
			return nil
		}
		rel := path.Clean(strings.TrimPrefix(filepath.ToSlash(a.Path), "/"))
		if rel == "." || strings.HasPrefix(rel, "../") {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("invalid path: %s", a.Path))
			return nil
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := ensureInsideRoot(abs); err != nil {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("access denied: %v", err))
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			return err
		}
		f, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.WriteString(a.Content); err != nil {
			return err
		}
		data, _ := os.ReadFile(abs)
		uri := relToURI(rel)
		mime := mimeByExt(abs)
		_ = w.AppendBlocks(
			resourceLink(uri, path.Base(rel), mime),
			embeddedText(uri, mime, string(data)),
		)
		return nil
	},
		mcpservice.WithToolDescription("Append text to a file. Returns a resource link and the full embedded contents after append."),
	)

	// Tool: fs.move — move/rename a file
	type MoveArgs struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	moveTool := mcpservice.NewTool[MoveArgs]("fs.move", func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[MoveArgs]) error {
		a := r.Args()
		if a.From == "" || a.To == "" {
			w.SetError(true)
			_ = w.AppendText("from and to are required")
			return nil
		}
		fromRel := path.Clean(strings.TrimPrefix(filepath.ToSlash(a.From), "/"))
		toRel := path.Clean(strings.TrimPrefix(filepath.ToSlash(a.To), "/"))
		if fromRel == "." || strings.HasPrefix(fromRel, "../") || toRel == "." || strings.HasPrefix(toRel, "../") {
			w.SetError(true)
			_ = w.AppendText("invalid path")
			return nil
		}
		fromAbs := filepath.Join(root, filepath.FromSlash(fromRel))
		toAbs := filepath.Join(root, filepath.FromSlash(toRel))
		if err := ensureInsideRoot(fromAbs); err != nil {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("access denied: %v", err))
			return nil
		}
		if err := ensureInsideRoot(toAbs); err != nil {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("access denied: %v", err))
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(toAbs), 0o750); err != nil {
			return err
		}
		if err := os.Rename(fromAbs, toAbs); err != nil {
			return err
		}
		uri := relToURI(toRel)
		_ = w.AppendBlocks(resourceLink(uri, path.Base(toRel), mimeByExt(toAbs)))
		return nil
	},
		mcpservice.WithToolDescription("Move or rename a file within the workspace."),
	)

	// Tool: fs.delete — delete a file
	type DeleteArgs struct {
		Path string `json:"path"`
	}
	deleteTool := mcpservice.NewTool[DeleteArgs]("fs.delete", func(ctx context.Context, _ sessions.Session, w mcpservice.ToolResponseWriter, r *mcpservice.ToolRequest[DeleteArgs]) error {
		a := r.Args()
		if a.Path == "" {
			w.SetError(true)
			_ = w.AppendText("path is required")
			return nil
		}
		rel := path.Clean(strings.TrimPrefix(filepath.ToSlash(a.Path), "/"))
		if rel == "." || strings.HasPrefix(rel, "../") {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("invalid path: %s", a.Path))
			return nil
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := ensureInsideRoot(abs); err != nil {
			w.SetError(true)
			_ = w.AppendText(fmt.Sprintf("access denied: %v", err))
			return nil
		}
		if err := os.Remove(abs); err != nil {
			return err
		}
		_ = w.AppendText(fmt.Sprintf("deleted %s", rel))
		return nil
	},
		mcpservice.WithToolDescription("Delete a file within the workspace."),
	)

	tools := mcpservice.NewStaticTools(readTool, writeTool, appendTool, moveTool, deleteTool)

	return mcpservice.NewServer(
		mcpservice.WithServerInfo(mcp.ImplementationInfo{Name: "examples-workspace-fs", Version: "0.1.0", Title: "Workspace FS"}),
		mcpservice.WithResourcesCapability(fsCap),
		mcpservice.WithToolsOptions(mcpservice.WithStaticToolsContainer(tools)),
	)
}

// mimeByExt returns a best-effort MIME type based on file extension.
func mimeByExt(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt":
		return "text/plain"
	case ".md":
		return "text/markdown"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".go":
		return "text/x-go"
	default:
		if ext != "" {
			// As a lightweight default, prefer octet-stream if unknown
			return "application/octet-stream"
		}
		return "application/octet-stream"
	}
}
