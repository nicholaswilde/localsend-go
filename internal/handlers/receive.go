package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/meowrain/localsend-go/internal/models"

	"github.com/meowrain/localsend-go/internal/utils/clipboard"
	"github.com/meowrain/localsend-go/internal/utils/logger"
	"github.com/schollz/progressbar/v3"
)

var (
	sessionIDCounter = 0
	sessionMutex     sync.Mutex
	fileNames        = make(map[string]string) // Used to save filenames
)

func PrepareReceive(w http.ResponseWriter, r *http.Request) {
	var req models.PrepareReceiveRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	logger.Infof("Received request from %s,device is %s", req.Info.Alias, req.Info.DeviceModel)

	sessionMutex.Lock()
	sessionIDCounter++
	sessionID := fmt.Sprintf("session-%d", sessionIDCounter)
	sessionMutex.Unlock()

	files := make(map[string]string)
	for fileID, fileInfo := range req.Files {
		token := fmt.Sprintf("token-%s", fileID)
		files[fileID] = token

		// Save filename
		fileNames[fileID] = fileInfo.FileName

		if strings.HasSuffix(fileInfo.FileName, ".txt") {
			logger.Success("TXT file content preview:", string(fileInfo.Preview))
			clipboard.WriteToClipBoard(fileInfo.Preview)
		}
	}

	resp := models.PrepareReceiveResponse{
		SessionID: sessionID,
		Files:     files,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func ReceiveHandler(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	fileID := r.URL.Query().Get("fileId")
	token := r.URL.Query().Get("token")

	// Validate request parameters
	if sessionID == "" || fileID == "" || token == "" {
		http.Error(w, "Missing parameters", http.StatusBadRequest)
		return
	}

	// Use fileID to get filename
	fileName, ok := fileNames[fileID]
	if !ok {
		http.Error(w, "Invalid file ID", http.StatusBadRequest)
		return
	}

	// Generate file path, preserve file extension
	filePath := filepath.Join("uploads", fileName)
	// Create directory (if it doesn't exist)
	dir := filepath.Dir(filePath)
	err := os.MkdirAll(dir, os.ModePerm)
	if err != nil {
		http.Error(w, "Failed to create directory", http.StatusInternalServerError)
		logger.Errorf("Error creating directory:", err)
		return
	}
	// Create file
	file, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Failed to create file", http.StatusInternalServerError)
		logger.Errorf("Error creating file:", err)
		return
	}
	defer file.Close()

	// Create a context to handle request cancellation
	ctx := r.Context()

	// After creating file, get file size
	contentLength := r.ContentLength

	// Create progress bar
	bar := progressbar.NewOptions64(
		contentLength,
		progressbar.OptionSetDescription(fmt.Sprintf("Downloading %s", fileName)),
		progressbar.OptionSetWidth(15),
		progressbar.OptionShowBytes(true),
		progressbar.OptionThrottle(time.Second), // Reduce refresh rate to reduce flickering
		progressbar.OptionShowCount(),
		progressbar.OptionClearOnFinish(), // Clear progress bar on finish
		progressbar.OptionSetRenderBlankState(true),
		progressbar.OptionSetPredictTime(true), // Predict remaining time
		progressbar.OptionFullWidth(),          // Use full width display
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "█", // Use solid block
			SaucerHead:    "█",
			SaucerPadding: "░", // Use gray block as background
			BarStart:      "|",
			BarEnd:        "|",
		}),
		progressbar.OptionOnCompletion(func() {
			fmt.Fprint(os.Stderr, "\n")
		}),
	)

	buffer := make([]byte, 2*1024*1024) // 2MB buffer

	// Use channel to handle transfer completion or cancellation
	done := make(chan error, 1)

	go func() {
		for {
			n, err := r.Body.Read(buffer)
			if err != nil && err != io.EOF {
				done <- fmt.Errorf("Failed to read file: %w", err)
				return
			}
			if n == 0 {
				done <- nil
				return
			}

			_, err = file.Write(buffer[:n])
			if err != nil {
				done <- fmt.Errorf("Failed to write file: %w", err)
				return
			}

			bar.Add(n)
		}
	}()

	// Wait for transfer completion or cancellation
	select {
	case err := <-done:
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			logger.Errorf("Transfer error:", err)
			// Delete incomplete file
			os.Remove(filePath)
			return
		}
	case <-ctx.Done():
		// Request cancelled
		logger.Info("Transfer cancelled")
		// Delete incomplete file
		os.Remove(filePath)
		// Close connection
		if conn, ok := w.(http.CloseNotifier); ok {
			conn.CloseNotify()
		}
		return
	}

	logger.Success("File saved to:", filePath)
	w.WriteHeader(http.StatusOK)
}
