// Package server — HTTP-слой: роуты, API-key авторизация, хендлеры.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"file-service-go/service"
)

// POST-защита: MaxBytesReader режет тело чуть выше лимита файла,
// точное значение проверяет Store.SaveStream.
const (
	bodySlack        = 1 << 20 // 1MB запас на multipart-фрейминг
	maxDescriptionLn = 4096
)

type Server struct {
	store     *service.Store
	keys      [][]byte // хэшировать нечего — сравниваем subtle.ConstantTimeCompare
	serverURL string
	maxFile   int64
}

func New(store *service.Store, apiKeys []string, serverURL string, maxFileSize int64) *Server {
	keys := make([][]byte, 0, len(apiKeys))
	for _, k := range apiKeys {
		keys = append(keys, []byte(k))
	}
	return &Server{store: store, keys: keys, serverURL: serverURL, maxFile: maxFileSize}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.Handle("POST /file/upload", s.requireKey(http.HandlerFunc(s.upload)))
	mux.HandleFunc("GET /file/{id}/info", s.info)
	mux.HandleFunc("GET /file/{id}/download", s.download)
	mux.Handle("DELETE /file/{id}", s.requireKey(http.HandlerFunc(s.delete)))
	return mux
}

// --- auth ---

// requireKey пускает дальше только запросы с валидным ключом.
// Ключ — в X-API-Key или Authorization: Bearer. Если список ключей пуст —
// загрузки/удаления выключены (fail closed), скачивание и info публичны.
func (s *Server) requireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.keys) == 0 {
			writeErr(w, http.StatusForbidden, "uploads disabled: no API keys configured")
			return
		}
		got := r.Header.Get("X-API-Key")
		if got == "" {
			if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				got = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		for _, k := range s.keys {
			if subtle.ConstantTimeCompare([]byte(got), k) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeErr(w, http.StatusUnauthorized, "invalid or missing API key")
	})
}

// --- handlers ---

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	total, quota, count := s.store.Usage()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok", "files": count, "bytes_used": total, "bytes_quota": quota,
	})
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxFile+bodySlack)
	mr, err := r.MultipartReader()
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "file exceeds size limit")
			return
		}
		writeErr(w, http.StatusBadRequest, "multipart form with \"file\" field expected")
		return
	}

	var (
		tmpPath, fileName, clientType, desc string
		fileSize                            int64
	)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeErr(w, http.StatusRequestEntityTooLarge, "file exceeds size limit")
				if tmpPath != "" {
					s.store.DiscardTemp(tmpPath)
				}
				return
			}
			writeErr(w, http.StatusBadRequest, "cannot read multipart body")
			return
		}
		switch part.FormName() {
		case "description":
			raw, _ := io.ReadAll(io.LimitReader(part, maxDescriptionLn+1))
			desc = string(raw)
			if len(desc) > maxDescriptionLn {
				desc = desc[:maxDescriptionLn]
			}
		case "file":
			if fileName != "" {
				writeErr(w, http.StatusBadRequest, "single file per request")
				return
			}
			fileName = part.FileName()
			if fileName == "" {
				writeErr(w, http.StatusBadRequest, "file must have a filename")
				return
			}
			clientType = part.Header.Get("Content-Type")
			tmpPath, fileSize, err = s.store.SaveStream(part, s.maxFile)
			if err != nil {
				status := http.StatusInternalServerError
				msg := "cannot save file"
				switch {
				case errors.Is(err, service.ErrTooLarge):
					status, msg = http.StatusRequestEntityTooLarge, "file exceeds size limit"
				case errors.Is(err, service.ErrNoFileUploaded):
					status, msg = http.StatusBadRequest, "uploaded file is empty"
				}
				writeErr(w, status, msg)
				return
			}
		}
	}
	if tmpPath == "" {
		writeErr(w, http.StatusBadRequest, "file must be specified")
		return
	}

	contentType := sniffType(tmpPath, clientType)
	meta, err := s.store.Commit(tmpPath, fileName, contentType, desc, fileSize)
	if err != nil {
		status := http.StatusInternalServerError
		msg := "cannot save file"
		if errors.Is(err, service.ErrQuotaExceeded) {
			status, msg = http.StatusRequestEntityTooLarge, "file exceeds storage quota"
		}
		writeErr(w, status, msg)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(toResponse(meta, s.downloadURL(r, meta.ID)))
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Stat(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(toResponse(m, s.downloadURL(r, m.ID)))
}

// download отдаёт тело стримингом через http.ServeContent:
// Content-Length, Accept-Ranges и Range-запросы из коробки, файл в RAM не грузится.
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	f, m, err := s.store.Open(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", m.Type)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, m.Name, m.CreatedAt, f)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, "file not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

type fileResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Size        int64  `json:"size"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

func toResponse(m *service.Metadata, url string) fileResponse {
	return fileResponse{
		ID: m.ID, Name: m.Name, Type: m.Type,
		Size: m.Size, Description: m.Description, URL: url,
	}
}

func (s *Server) downloadURL(r *http.Request, id string) string {
	if s.serverURL != "" {
		return s.serverURL + "/file/" + id + "/download"
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/file/" + id + "/download"
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// sniffType определяет тип по первым байтам; заголовку клиента доверяем
// только если сниффер дал generic octet-stream.
func sniffType(tmpPath, clientType string) string {
	sniffed := sniffHead(tmpPath)
	if sniffed == "" || sniffed == "application/octet-stream" {
		if clientType != "" {
			return clientType
		}
	}
	if sniffed != "" {
		return sniffed
	}
	return "application/octet-stream"
}

func sniffHead(tmpPath string) string {
	f, err := os.Open(tmpPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	var head [512]byte
	n, _ := f.Read(head[:])
	if n == 0 {
		return ""
	}
	return http.DetectContentType(head[:n])
}
