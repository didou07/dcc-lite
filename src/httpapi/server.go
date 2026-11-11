package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"sync/atomic"

	"github.com/didou07/dcc-lite/src/app"
	webui "github.com/didou07/dcc-lite/src/web"
)

type Server struct {
	app *app.App

	remote *remoteMode

	names nameCache

	scanning atomic.Bool
}

func New(application *app.App) http.Handler {
	s := &Server{app: application}
	mux := s.routes()

	mux.HandleFunc("GET /api/v1/remote-access", s.getRemoteAccess)
	mux.HandleFunc("PUT /api/v1/remote-access", s.putRemoteAccess)
	mux.HandleFunc("POST /api/v1/remote-access/password", s.setRemotePassword)

	mux.HandleFunc("GET /api/v1/profiles/{id}/password", s.revealCredential)
	return secureHeaders(localOnly(mux))
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/system", s.system)
	mux.HandleFunc("GET /api/v1/profiles", s.listProfiles)
	mux.HandleFunc("GET /api/v1/network/ranges", s.networkRanges)
	mux.HandleFunc("POST /api/v1/network/scan", s.networkScan)
	mux.HandleFunc("GET /api/v1/connections", s.listConnections)
	mux.HandleFunc("POST /api/v1/profiles/{id}/test", s.testConnection)
	mux.HandleFunc("GET /api/v1/profiles/{id}/screen", s.deviceScreen)
	mux.HandleFunc("POST /api/v1/profiles/{id}/remote-key", s.deviceRemoteKey)
	mux.HandleFunc("POST /api/v1/profiles/{id}/message", s.deviceMessage)
	mux.HandleFunc("POST /api/v1/profiles/{id}/remote-text", s.deviceRemoteText)
	mux.HandleFunc("GET /api/v1/profiles/{id}/channels", s.deviceChannels)
	mux.HandleFunc("POST /api/v1/profiles/{id}/zap", s.deviceZap)
	mux.HandleFunc("GET /api/v1/profiles/{id}/current", s.deviceCurrent)
	mux.HandleFunc("GET /api/v1/profiles/{id}/picon", s.devicePicon)
	mux.HandleFunc("GET /api/v1/profiles/{id}/epgnow", s.deviceEPGNow)
	mux.HandleFunc("GET /api/v1/profiles/{id}/epg", s.deviceEPG)
	mux.HandleFunc("GET /api/v1/profiles/{id}/epgsearch", s.deviceEPGSearch)
	mux.HandleFunc("GET /api/v1/profiles/{id}/timers", s.deviceTimers)
	mux.HandleFunc("POST /api/v1/profiles/{id}/timers", s.deviceTimerAdd)
	mux.HandleFunc("POST /api/v1/profiles/{id}/timers/toggle", s.deviceTimerToggle)
	mux.HandleFunc("POST /api/v1/profiles/{id}/timers/delete", s.deviceTimerDelete)
	mux.HandleFunc("POST /api/v1/profiles/{id}/timers/cleanup", s.deviceTimerCleanup)
	mux.HandleFunc("POST /api/v1/profiles/{id}/timers/change", s.deviceTimerChange)
	mux.HandleFunc("POST /api/v1/profiles/{id}/record-now", s.deviceRecordNow)
	mux.HandleFunc("GET /api/v1/profiles/{id}/sleeptimer", s.deviceSleepTimerGet)
	mux.HandleFunc("POST /api/v1/profiles/{id}/sleeptimer", s.deviceSleepTimerSet)
	mux.HandleFunc("GET /api/v1/profiles/{id}/epgsimilar", s.deviceEPGSimilar)
	mux.HandleFunc("GET /api/v1/profiles/{id}/movie-locations", s.deviceMovieLocations)
	mux.HandleFunc("POST /api/v1/profiles/{id}/movies/rename", s.deviceMovieRename)
	mux.HandleFunc("POST /api/v1/profiles/{id}/movies/move", s.deviceMovieMove)
	mux.HandleFunc("GET /api/v1/profiles/{id}/movies", s.deviceMovies)
	mux.HandleFunc("POST /api/v1/profiles/{id}/movies/delete", s.deviceMovieDelete)
	mux.HandleFunc("GET /api/v1/profiles/{id}/movie.m3u", s.deviceMovieStream)
	mux.HandleFunc("GET /api/v1/profiles/{id}/stream.m3u", s.deviceStream)
	mux.HandleFunc("GET /api/v1/profiles/{id}/info", s.deviceInfoHandler)
	mux.HandleFunc("GET /api/v1/profiles/{id}/signal", s.deviceSignal)
	mux.HandleFunc("GET /api/v1/files/sessions/{id}/enigma2/backup", s.backupEnigma2)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/enigma2/restore", s.restoreEnigma2)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/ipk", s.uploadIPK)
	mux.HandleFunc("POST /api/v1/profiles/{id}/power", s.devicePower)
	mux.HandleFunc("GET /api/v1/profiles/{id}/multiboot", s.deviceMultiboot)
	mux.HandleFunc("POST /api/v1/profiles/{id}/multiboot", s.deviceMultibootSet)
	mux.HandleFunc("POST /api/v1/terminal/sessions", s.openTerminal)
	mux.HandleFunc("GET /api/v1/terminal/sessions/{id}", s.terminalStatus)
	mux.HandleFunc("GET /api/v1/terminal/sessions/{id}/ws", s.terminalWebSocket)
	mux.HandleFunc("DELETE /api/v1/terminal/sessions/{id}", s.closeTerminal)
	mux.HandleFunc("GET /api/v1/files/local/list", s.listLocalFiles)
	mux.HandleFunc("POST /api/v1/files/local/mkdir", s.makeLocalDirectory)
	mux.HandleFunc("POST /api/v1/files/local/rename", s.renameLocalFile)
	mux.HandleFunc("POST /api/v1/files/local/remove", s.removeLocalFile)
	mux.HandleFunc("POST /api/v1/files/local/chmod", s.chmodLocalFile)
	mux.HandleFunc("GET /api/v1/files/sessions", s.listFileSessions)
	mux.HandleFunc("POST /api/v1/files/sessions", s.openFileSession)
	mux.HandleFunc("DELETE /api/v1/files/sessions/{id}", s.closeFileSession)
	mux.HandleFunc("GET /api/v1/files/sessions/{id}/list", s.listRemoteFiles)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/mkdir", s.makeRemoteDirectory)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/rename", s.renameRemoteFile)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/remove", s.removeRemoteFile)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/chmod", s.chmodRemoteFile)
	mux.HandleFunc("GET /api/v1/files/sessions/{id}/content", s.readRemoteText)
	mux.HandleFunc("PUT /api/v1/files/sessions/{id}/content", s.writeRemoteText)
	mux.HandleFunc("GET /api/v1/files/sessions/{id}/enigma2/bouquets", s.listBouquets)
	mux.HandleFunc("GET /api/v1/files/sessions/{id}/enigma2/bouquet", s.getBouquet)
	mux.HandleFunc("PUT /api/v1/files/sessions/{id}/enigma2/bouquet", s.saveBouquet)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/enigma2/bouquet/new", s.newBouquet)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/enigma2/bouquet/list-change", s.changeIndex)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/enigma2/reload", s.reloadBouquets)
	mux.HandleFunc("GET /api/v1/files/sessions/{id}/enigma2/services", s.listServices)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/transfer", s.startFileTransfer)
	mux.HandleFunc("GET /api/v1/files/transfers", s.listFileTransfers)
	mux.HandleFunc("DELETE /api/v1/files/transfers", s.clearFinishedFileTransfers)
	mux.HandleFunc("GET /api/v1/files/transfers/{id}", s.getFileTransfer)
	mux.HandleFunc("DELETE /api/v1/files/transfers/{id}", s.cancelFileTransfer)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/upload", s.uploadFromBrowser)
	mux.HandleFunc("POST /api/v1/files/sessions/{id}/download-prepare", s.prepareBrowserDownload)
	mux.HandleFunc("GET /api/v1/files/transfers/{id}/file", s.fetchStagedDownload)
	mux.HandleFunc("POST /api/v1/files/transfers/{id}/resume", s.resumeFileTransfer)
	mux.HandleFunc("POST /api/v1/files/transfers/{id}/restart", s.restartFileTransfer)
	mux.HandleFunc("PUT /api/v1/profiles/{id}/password", s.setCredential)
	mux.HandleFunc("DELETE /api/v1/profiles/{id}/password", s.deleteCredential)

	mux.HandleFunc("PUT /api/v1/profiles/{id}/credential", s.setCredential)
	mux.HandleFunc("DELETE /api/v1/profiles/{id}/credential", s.deleteCredential)
	mux.HandleFunc("POST /api/v1/profiles", s.createProfile)
	mux.HandleFunc("PUT /api/v1/profiles/{id}", s.updateProfile)
	mux.HandleFunc("DELETE /api/v1/profiles/{id}", s.deleteProfile)
	mux.Handle("/", s.staticFiles())
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.app.Version, "api": "v1"})
}

func (s *Server) system(w http.ResponseWriter, _ *http.Request) {
	info := s.app.SystemInfo()
	if s.remote != nil {
		info["remote"] = "true"
	}
	writeJSON(w, http.StatusOK, info)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func (s *Server) staticFiles() http.Handler {
	sub, err := fs.Sub(webui.FS(), ".")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/index.html") {
			page, err := webui.Page("index.html")
			if err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("X-DCC-Build", webui.Build())
				_, _ = w.Write(page)
				return
			}
		}
		if r.URL.Query().Get("v") == webui.Build() {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
