package handlers

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/meowrain/localsend-go/internal/discovery"
	"github.com/meowrain/localsend-go/internal/discovery/shared"
	"github.com/meowrain/localsend-go/internal/models"
	"github.com/meowrain/localsend-go/internal/tui"
	"github.com/meowrain/localsend-go/internal/utils/logger"
	"github.com/meowrain/localsend-go/internal/utils/sha256"
	"github.com/schollz/progressbar/v3"
)

// SendFileToOtherDevicePrepare function
func SendFileToOtherDevicePrepare(ip string, path string) (*models.PrepareReceiveResponse, error) {
	// Prepare metadata for all files
	files := make(map[string]models.FileInfo)
	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			sha256Hash, err := sha256.CalculateSHA256(filePath)
			if err != nil {
				return fmt.Errorf("error calculating SHA256 hash: %w", err)
			}
			fileMetadata := models.FileInfo{
				ID:       info.Name(), // Use filename as ID
				FileName: info.Name(),
				Size:     info.Size(),
				FileType: filepath.Ext(filePath),
				SHA256:   sha256Hash,
			}
			files[fileMetadata.ID] = fileMetadata
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error walking the path: %w", err)
	}

	// Create and populate PrepareReceiveRequest struct
	request := models.PrepareReceiveRequest{
		Info: models.Info{
			Alias:       shared.Message.Alias,
			Version:     shared.Message.Version,
			DeviceModel: shared.Message.DeviceModel,
			DeviceType:  shared.Message.DeviceType,
			Fingerprint: shared.Message.Fingerprint,
			Port:        shared.Message.Port,
			Protocol:    shared.Message.Protocol,
			Download:    shared.Message.Download,
		},
		Files: files,
	}

	// Encode request struct to JSON
	requestJson, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("error encoding request to JSON: %w", err)
	}

	// Send POST request
	url := fmt.Sprintf("https://%s:53317/api/localsend/v2/prepare-upload", ip)
	client := &http.Client{
		Timeout: 60 * time.Second, // Transfer timeout
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Ignore TLS
			},
		},
	}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(requestJson))
	if err != nil {
		return nil, fmt.Errorf("error sending POST request: %w", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 204:
			return nil, fmt.Errorf("finished (No file transfer needed)")
		case 400:
			return nil, fmt.Errorf("invalid body")
		case 403:
			return nil, fmt.Errorf("rejected")
		case 500:
			return nil, fmt.Errorf("unknown error by receiver")
		}
		return nil, fmt.Errorf("failed to send metadata: received status code %d", resp.StatusCode)
	}

	// Decode response JSON to PrepareReceiveResponse struct
	var prepareReceiveResponse models.PrepareReceiveResponse
	if err := json.NewDecoder(resp.Body).Decode(&prepareReceiveResponse); err != nil {
		return nil, fmt.Errorf("error decoding response JSON: %w", err)
	}

	return &prepareReceiveResponse, nil
}

// uploadFile function
func uploadFile(ctx context.Context, ip, sessionId, fileId, token, filePath string) error {
	// Open file to send
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("error opening file: %w", err)
	}
	defer file.Close()

	// Get file size for progress bar
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("error getting file info: %w", err)
	}
	fileSize := fileInfo.Size()

	// Create progress bar
	bar := progressbar.NewOptions64(
		fileSize,
		progressbar.OptionSetDescription(fmt.Sprintf("Uploading %s", filepath.Base(filePath))),
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

	// Build file upload URL
	uploadURL := fmt.Sprintf("https://%s:53317/api/localsend/v2/upload?sessionId=%s&fileId=%s&token=%s",
		ip, sessionId, fileId, token)

	// Use pipe to avoid loading entire file into memory
	pr, pw := io.Pipe()

	// Create an error channel to pass errors during upload
	uploadErr := make(chan error, 1)

	go func() {
		defer pw.Close()
		// Write file data in a new goroutine
		_, err := io.Copy(io.MultiWriter(pw, bar), file)
		if err != nil {
			uploadErr <- err
			return
		}
	}()

	// Create HTTP client with TLS config
	client := &http.Client{
		Timeout: 30 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Skip certificate verification
			},
			MaxIdleConns:       100,
			IdleConnTimeout:    90 * time.Second,
			DisableCompression: true,
		},
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", uploadURL, pr)
	if err != nil {
		return fmt.Errorf("error creating POST request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = fileSize

	// Use custom client to send request, instead of http.DefaultClient
	resp, err := client.Do(req)

	// Check if cancelled
	select {
	case <-ctx.Done():
		return fmt.Errorf("Transfer cancelled")
	case err := <-uploadErr:
		if err != nil {
			return fmt.Errorf("Upload error: %w", err)
		}
	default:
		if err != nil {
			return fmt.Errorf("error sending file upload request: %w", err)
		}
	}

	// 检查响应
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case 400:
			return fmt.Errorf("missing parameters")
		case 403:
			return fmt.Errorf("invalid token or IP address")
		case 409:
			return fmt.Errorf("blocked by another session")
		case 500:
			return fmt.Errorf("unknown error by receiver")
		}
		return fmt.Errorf("file upload failed: received status code %d", resp.StatusCode)
	}

	fmt.Println() // Add newline to make the progress bar clearer
	logger.Success("File uploaded successfully")
	return nil
}

// SendFile function
func SendFile(path string) error {
	updates := make(chan []models.SendModel)
	discovery.ListenAndStartBroadcasts(updates)
	fmt.Println("Please select a device you want to send file to:")
	ip, err := tui.SelectDevice(updates)
	if err != nil {
		return err
	}
	response, err := SendFileToOtherDevicePrepare(ip, path)
	if err != nil {
		return err
	}

	// Create a context for cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use shared HTTP server to handle cancel requests
	logger.Info("Registering cancel handler for session: ", response.SessionID)
	RegisterCancelHandler(response.SessionID, cancel)
	defer UnregisterCancelHandler(response.SessionID)

	// Iterate through directory and files
	err = filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			fileId := info.Name()
			token, ok := response.Files[fileId]
			if !ok {
				return fmt.Errorf("token not found for file: %s", fileId)
			}
			err = uploadFile(ctx, ip, response.SessionID, fileId, token, filePath)
			if err != nil {
				return fmt.Errorf("error uploading file: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error walking the path: %w", err)
	}

	return nil
}

func NormalSendHandler(w http.ResponseWriter, r *http.Request) {
	logger.Info("Handling upload request...") // Debug log - request start

	// Limit form data size (set to 10 MB here, adjust as needed)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, fmt.Sprintf("Failed to parse form: %v", err), http.StatusBadRequest)
		return
	}

	// Get uploaded directory name (from frontend hidden input)
	uploadedDirName := r.FormValue("directoryName")
	logger.Debugf("directoryName from form: '%s'\n", uploadedDirName) // Debug log - directoryName value

	// Get all uploaded files
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		http.Error(w, "No files uploaded", http.StatusBadRequest)
		return
	}

	uploadDir := "./uploads"    // Base upload directory
	finalUploadDir := uploadDir // Default final upload directory

	// If frontend provides directory name and it is not empty, create subdirectory named after it
	if uploadedDirName != "" {
		finalUploadDir = filepath.Join(uploadDir, uploadedDirName)
	} else {
		logger.Debug("No directoryName provided, uploading to root uploads dir.") // Debug log - no directoryName
	}
	logger.Debugf("Final upload directory: '%s'\n", finalUploadDir)

	// Create final upload directory (if it doesn't exist)
	if err := os.MkdirAll(finalUploadDir, os.ModePerm); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create upload directory: %v", err), http.StatusInternalServerError)
		return
	}

	// Iterate through all files to save
	for _, fileHeader := range files {
		// Open uploaded file
		file, err := fileHeader.Open()
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to open file: %v", err), http.StatusInternalServerError)
			return
		}
		defer file.Close()

		// Join target path (use finalUploadDir as root)
		destPath := filepath.Join(finalUploadDir, fileHeader.Filename)
		logger.Infof("Saving file '%s' to destPath: '%s'\n", fileHeader.Filename, destPath) // Debug log - file dest path

		// Create target directory (if it doesn't exist)
		if err := os.MkdirAll(filepath.Dir(destPath), os.ModePerm); err != nil {
			http.Error(w, fmt.Sprintf("Failed to create directory: %v", err), http.StatusInternalServerError)
			return
		}

		// Create target file
		dst, err := os.Create(destPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create file: %v", err), http.StatusInternalServerError)
			return
		}
		defer dst.Close()

		// Write uploaded file content to target file
		if _, err := io.Copy(dst, file); err != nil {
			http.Error(w, fmt.Sprintf("Failed to save file: %v", err), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, "Files uploaded successfully, total %d files, uploaded to directory: %s\n", len(files), finalUploadDir)
}
