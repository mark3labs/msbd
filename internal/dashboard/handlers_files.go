package dashboard

// handlers_files.go — the sandbox file browser: navigate, view, edit, upload,
// download, create and delete. Every operation goes through core's guest-side
// filesystem API (never the host filesystem).

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
)

// maxViewBytes caps how much of a file we will render in the viewer/editor.
const maxViewBytes = 256 * 1024

// maxUploadBytes caps a single uploaded file.
const maxUploadBytes = 32 << 20 // 32 MiB

type filesSignals struct {
	Path     string `json:"filepath"`
	Name     string `json:"filename"`
	Contents string `json:"fileedit"`
	NewDir   string `json:"newdir"`
}

func (h *Handler) filesList(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sig := &filesSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	dir := cleanDir(sig.Path)
	entries, err := h.svc.ListDir(r.Context(), id, dir, "")
	if failInline(sse, "detail-error", "List files", err) {
		return
	}
	rows := make([]views.FileRow, 0, len(entries))
	for _, e := range entries {
		name := path.Base(e.Path)
		rows = append(rows, views.FileRow{
			Name:   name,
			Path:   e.Path,
			Kind:   e.Kind,
			IsDir:  e.Kind == "directory",
			Hidden: strings.HasPrefix(name, "."),
			Size:   views.HumanBytes(uint64(maxZero(e.Size))),
			Mode:   fmt.Sprintf("%#o", e.Mode),
		})
	}
	// Directories first, then case-insensitive by name.
	sortRows(rows, views.TableSort{Dir: "asc"}, func(a, b views.FileRow) bool {
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	_ = sse.PatchElementTempl(views.FilesPanel(id, dir, crumbsFor(dir), rows))
}

// crumbsFor builds the breadcrumb trail for an absolute guest path.
func crumbsFor(dir string) []views.Crumb {
	out := []views.Crumb{{Label: "/", Path: "/"}}
	var cur strings.Builder
	for seg := range strings.SplitSeq(strings.Trim(dir, "/"), "/") {
		if seg == "" {
			continue
		}
		cur.WriteString("/")
		cur.WriteString(seg)
		out = append(out, views.Crumb{Label: seg, Path: cur.String()})
	}
	return out
}

func cleanDir(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	return path.Clean(p)
}

// filesView reads a file into the modal viewer/editor. Binary or oversized
// files are shown read-only with an explanatory note instead of corrupting the
// buffer on save.
func (h *Handler) filesView(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	sse := datastar.NewSSE(w, r)

	data, err := h.svc.ReadFile(r.Context(), id, p, "")
	if failInline(sse, "detail-error", "Read file", err) {
		return
	}

	note, editable, body := "", true, string(data)
	switch {
	case len(data) > maxViewBytes:
		note = fmt.Sprintf("File is %s — showing the first %s, read-only.",
			views.HumanBytes(uint64(len(data))), views.HumanBytes(maxViewBytes))
		editable = false
		body = string(data[:maxViewBytes])
	case !utf8.Valid(data):
		note = "This looks like a binary file — showing a read-only preview. Use Download for the real bytes."
		editable = false
		body = previewBinary(data)
	}

	if editable {
		_ = sse.MarshalAndPatchSignals(&filesSignals{Contents: body, Name: p})
	} else {
		_ = sse.MarshalAndPatchSignals(&filesSignals{Name: p})
	}
	_ = sse.PatchElementTempl(views.FileViewContent(id, p, body, editable, note))
}

// previewBinary renders a short hex/ASCII preview of non-UTF8 content.
func previewBinary(data []byte) string {
	const max = 2048
	if len(data) > max {
		data = data[:max]
	}
	var b strings.Builder
	for i := 0; i < len(data); i += 16 {
		end := min(i+16, len(data))
		fmt.Fprintf(&b, "%08x  ", i)
		for j := i; j < i+16; j++ {
			if j < end {
				fmt.Fprintf(&b, "%02x ", data[j])
			} else {
				b.WriteString("   ")
			}
		}
		b.WriteString(" |")
		for j := i; j < end; j++ {
			if data[j] >= 0x20 && data[j] < 0x7f {
				b.WriteByte(data[j])
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|\n")
	}
	return b.String()
}

func (h *Handler) filesSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sig := &filesSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	p := strings.TrimSpace(sig.Name)
	if p == "" {
		notify(sse, toast.VariantWarning, "Save", "no file is open")
		return
	}
	if notifyErr(sse, "Save file", h.svc.WriteFile(r.Context(), id, p, "", []byte(sig.Contents))) {
		return
	}
	closeNative(sse, "file-view")
	notify(sse, toast.VariantSuccess, "Saved", p)
	h.repaintFiles(r, sse, id, sig.Path)
}

func (h *Handler) filesMkdir(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sig := &filesSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	name := strings.TrimSpace(sig.NewDir)
	if name == "" {
		notify(sse, toast.VariantWarning, "New folder", "name is required")
		return
	}
	target := path.Join(cleanDir(sig.Path), name)
	if notifyErr(sse, "Create folder", h.svc.Mkdir(r.Context(), id, target, "")) {
		return
	}
	closeNative(sse, "new-folder")
	_ = sse.MarshalAndPatchSignals(&filesSignals{NewDir: ""})
	notify(sse, toast.VariantSuccess, "Folder created", target)
	h.repaintFiles(r, sse, id, sig.Path)
}

func (h *Handler) filesRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	sig := &filesSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	if notifyErr(sse, "Delete", h.svc.Remove(r.Context(), id, p, "", true)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Deleted", p)
	h.repaintFiles(r, sse, id, sig.Path)
}

// filesDownload streams a guest file to the browser as an attachment.
func (h *Handler) filesDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	data, err := h.svc.ReadFile(r.Context(), id, p, "")
	if err != nil {
		http.Error(w, cleanErr(err), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+safeFilename(path.Base(p))+"\"")
	_, _ = w.Write(data)
}

// filesUpload writes browser-selected files into the guest. This is a plain
// multipart POST (not a Datastar action) because file bytes do not belong in
// the signal store.
func (h *Handler) filesUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "bad upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := cleanDir(r.FormValue("dir"))
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file", http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxUploadBytes))
	if err != nil {
		http.Error(w, "read failed", http.StatusInternalServerError)
		return
	}
	target := path.Join(dir, path.Base(hdr.Filename))
	if err := h.svc.WriteFile(r.Context(), id, target, "", data); err != nil {
		http.Error(w, cleanErr(err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// repaintFiles re-renders the listing for the directory currently on screen.
func (h *Handler) repaintFiles(r *http.Request, sse *datastar.ServerSentEventGenerator, id, dir string) {
	d := cleanDir(dir)
	entries, err := h.svc.ListDir(r.Context(), id, d, "")
	if err != nil {
		return
	}
	rows := make([]views.FileRow, 0, len(entries))
	for _, e := range entries {
		name := path.Base(e.Path)
		rows = append(rows, views.FileRow{
			Name:   name,
			Path:   e.Path,
			Kind:   e.Kind,
			IsDir:  e.Kind == "directory",
			Hidden: strings.HasPrefix(name, "."),
			Size:   views.HumanBytes(uint64(maxZero(e.Size))),
			Mode:   fmt.Sprintf("%#o", e.Mode),
		})
	}
	sortRows(rows, views.TableSort{Dir: "asc"}, func(a, b views.FileRow) bool {
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	_ = sse.PatchElementTempl(views.FilesPanel(id, d, crumbsFor(d), rows))
}
