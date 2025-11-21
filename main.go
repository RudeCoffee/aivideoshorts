package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	_ "image"
	_ "image/png"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	pigo "github.com/esimov/pigo/core"
)

type videoDimensions struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Keyframe struct {
	Time  float64 `json:"time"`
	CropX int     `json:"cropX"`
}

func getVideoDimensions(videoPath string) (*videoDimensions, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "json", videoPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffprobe error: %s\n%s", err, output)
	}

	var data struct {
		Streams []videoDimensions `json:"streams"`
	}
	if err := json.Unmarshal(output, &data); err != nil {
		return nil, err
	}

	if len(data.Streams) == 0 {
		return nil, fmt.Errorf("no video streams found")
	}

	return &data.Streams[0], nil
}

func getVideoFrameRate(videoPath string) (float64, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=r_frame_rate", "-of", "default=noprint_wrappers=1:nokey=1", videoPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ffprobe error getting frame rate: %s\n%s", err, output)
	}

	trimmedOutput := strings.TrimSpace(string(output))
	parts := strings.Split(trimmedOutput, "/")
	if len(parts) == 1 {
		rate, err := strconv.ParseFloat(trimmedOutput, 64)
		if err != nil {
			return 0, fmt.Errorf("could not parse frame rate: %s", trimmedOutput)
		}
		return rate, nil
	}
	if len(parts) == 2 {
		num, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		den, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
		if den == 0 {
			return 0, fmt.Errorf("invalid frame rate denominator")
		}
		return num / den, nil
	}

	return 0, fmt.Errorf("unexpected frame rate format: %s", trimmedOutput)
}

// TranscriptSegment represents a single segment of the video transcript.
type TranscriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// UploadResponse is the structure of the JSON response sent after a video upload.
type UploadResponse struct {
	Transcript []TranscriptSegment `json:"transcript"`
	VideoFile  string              `json:"videoFile"`
}

func main() {
	// Check if ffmpeg is installed.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Fatal("ffmpeg is not installed or not in the system's PATH.")
	}

	fsStatic := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fsStatic))

	fsUploads := http.FileServer(http.Dir("uploads"))
	http.Handle("/uploads/", http.StripPrefix("/uploads/", fsUploads))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "templates/index.html")
	})
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/clip", clipHandler)
	http.HandleFunc("/autoclip", autoClipHandler)
	http.HandleFunc("/first-frame", firstFrameHandler)
	http.HandleFunc("/manual-clip", manualClipHandler)

	log.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 32 MB is the default used by FormFile
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "Failed to parse multipart form", http.StatusInternalServerError)
		return
	}

	file, handler, err := r.FormFile("video")
	if err != nil {
		http.Error(w, "Invalid video file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Create the uploads directory if it doesn't exist
	if _, err := os.Stat("uploads"); os.IsNotExist(err) {
		os.Mkdir("uploads", os.ModeDir)
	}

	// Create a new file in the uploads directory
	dst, err := os.Create(filepath.Join("uploads", handler.Filename))
	if err != nil {
		http.Error(w, "Failed to create file on server", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy the uploaded file to the destination file
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Failed to save file on server", http.StatusInternalServerError)
		return
	}

	transcript, err := transcribeVideo(dst.Name())
	if err != nil {
		http.Error(w, "Failed to transcribe video", http.StatusInternalServerError)
		return
	}

	response := UploadResponse{
		Transcript: transcript,
		VideoFile:  filepath.Base(dst.Name()),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func clipHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	start := r.FormValue("start")
	end := r.FormValue("end")
	videoFile := r.FormValue("videoFile")

	if start == "" || end == "" || videoFile == "" {
		http.Error(w, "Missing required form values: start, end, and videoFile", http.StatusBadRequest)
		return
	}

	// Create a unique name for the clip
	clipFile := fmt.Sprintf("clip-%s-%s-%s", start, end, videoFile)
	clipPath := filepath.Join("static", clipFile)

	cmd := exec.Command("ffmpeg", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-to", end, "-c:v", "copy", "-c:a", "aac", "-af", "loudnorm", clipPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "/static/%s", clipFile)
}

func autoClipHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	start := r.FormValue("start")
	end := r.FormValue("end")
	videoFile := r.FormValue("videoFile")
	cropXStr := r.FormValue("cropX")

	if start == "" || end == "" || videoFile == "" {
		http.Error(w, "Missing required form values: start, end, and videoFile", http.StatusBadRequest)
		return
	}

	clipFile := fmt.Sprintf("autoclip-%s-%s-%s", start, end, videoFile)
	clipPath := filepath.Join("static", clipFile)

	dims, err := getVideoDimensions(filepath.Join("uploads", videoFile))
	if err != nil {
		log.Printf("Failed to get video dimensions: %s", err)
		http.Error(w, "Failed to get video dimensions", http.StatusInternalServerError)
		return
	}

	cropWidth := dims.Height * 9 / 16

	// Calculate crop expression
	var cropExpr string

	if cropXStr != "" {
		// Manual fixed crop
		manualCropX, err := strconv.Atoi(cropXStr)
		if err != nil {
			http.Error(w, "Invalid cropX value", http.StatusBadRequest)
			return
		}
		cropX := manualCropX - (cropWidth / 2)
		// Clamp
		if cropX < 0 {
			cropX = 0
		}
		if cropX+cropWidth > dims.Width {
			cropX = dims.Width - cropWidth
		}
		cropExpr = fmt.Sprintf("%d", cropX)
	} else {
		// Smooth face tracking
		// 1. Ensure cascade file exists
		cascadePath := filepath.Join("cascade", "facefinder")
		if _, err := os.Stat(cascadePath); os.IsNotExist(err) {
			if err := downloadCascadeFile(cascadePath); err != nil {
				log.Printf("Failed to download cascade file: %s", err)
				http.Error(w, "Failed to download cascade file", http.StatusInternalServerError)
				return
			}
		}

		// 2. Extract frames
		// Calculate duration to determine FPS
		startSec, err := parseVTTTimestamp(start)
		if err != nil {
			log.Printf("Failed to parse start time: %s", err)
			http.Error(w, "Invalid start time", http.StatusBadRequest)
			return
		}
		endSec, err := parseVTTTimestamp(end)
		if err != nil {
			log.Printf("Failed to parse end time: %s", err)
			http.Error(w, "Invalid end time", http.StatusBadRequest)
			return
		}
		duration := endSec - startSec
		if duration <= 0 {
			duration = 10 // fallback
		}

		// Target 10 FPS for smooth tracking
		fps := 10.0
		log.Printf("Auto-tracking: duration=%.2fs, fps=%.2f", duration, fps)

		frameDir, framePaths, err := extractFrames(filepath.Join("uploads", videoFile), start, end, fps)
		if err != nil {
			log.Printf("Failed to extract frames: %s", err)
			http.Error(w, "Failed to extract frames", http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(frameDir)

		// 3. Detect faces
		keyframes, err := detectFacesInFrames(framePaths, cascadePath, fps, dims.Width)
		if err != nil {
			log.Printf("Failed to detect faces: %s", err)
			// Fallback to center
			cropExpr = fmt.Sprintf("%d", (dims.Width-cropWidth)/2)
		} else {
			// 4. Smooth path
			smoothedKeyframes := smoothPath(keyframes, dims.Width, cropWidth)

			// Shift keyframes by start time because ffmpeg -ss output option preserves original timestamps
			for i := range smoothedKeyframes {
				smoothedKeyframes[i].Time += startSec
			}

			// 5. Build expression
			cropExpr = buildCropExpression(smoothedKeyframes, fps)
		}
	}

	subtitlePath := filepath.ToSlash(filepath.Join("uploads", strings.TrimSuffix(videoFile, filepath.Ext(videoFile))+".vtt"))
	// Use the dynamic crop expression
	vf_string := fmt.Sprintf("crop=%d:%d:%s:0,scale=1080:1920,setsar=1,subtitles=%s:force_style='Alignment=2\\,FontName=Arial\\,FontSize=18\\,PrimaryColour=&Hffffff\\,BackColor=&H80000000\\,BorderStyle=1\\,Outline=1\\,Shadow=0\\,MarginV=50'", cropWidth, dims.Height, cropExpr, strings.ReplaceAll(subtitlePath, "\\", "/"))

	// Write filter to temporary file to avoid command line length limits
	filterFile, err := os.CreateTemp("", "filter_*.txt")
	if err != nil {
		log.Printf("Failed to create filter file: %s", err)
		http.Error(w, "Failed to create filter file", http.StatusInternalServerError)
		return
	}
	defer os.Remove(filterFile.Name())
	defer filterFile.Close()

	if _, err := filterFile.WriteString(vf_string); err != nil {
		log.Printf("Failed to write to filter file: %s", err)
		http.Error(w, "Failed to write to filter file", http.StatusInternalServerError)
		return
	}
	filterFile.Close() // Close explicitly before ffmpeg reads it

	log.Printf("Running ffmpeg with filter script: %s", filterFile.Name())
	cmd := exec.Command("ffmpeg", "-y", "-threads", "0", "-i", filepath.Join("uploads", videoFile), "-filter_script:v", filterFile.Name(), "-c:a", "aac", "-af", "loudnorm", "-ss", start, "-to", end, clipPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "/static/%s", clipFile)
}

func firstFrameHandler(w http.ResponseWriter, r *http.Request) {
	videoFile := r.URL.Query().Get("videoFile")
	start := r.URL.Query().Get("start")

	if videoFile == "" || start == "" {
		http.Error(w, "Missing required query parameters: videoFile and start", http.StatusBadRequest)
		return
	}

	frameDir := filepath.Join("static", "frames")
	if err := os.MkdirAll(frameDir, os.ModePerm); err != nil {
		http.Error(w, "Failed to create frame directory", http.StatusInternalServerError)
		return
	}

	framePath := filepath.Join(frameDir, fmt.Sprintf("first-frame-%s.png", start))
	// Use -y to overwrite existing frame, helpful for dev
	cmd := exec.Command("ffmpeg", "-y", "-threads", "0", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-vframes", "1", framePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error extracting first frame: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to extract first frame: %s", output), http.StatusInternalServerError)
		return
	}

	// Return the path to the frame so the frontend can display it
	fmt.Fprintf(w, "/%s", filepath.ToSlash(framePath))
}

func manualClipHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	start := r.FormValue("start")
	end := r.FormValue("end")
	videoFile := r.FormValue("videoFile")
	keyframesJSON := r.FormValue("keyframes")

	if start == "" || end == "" || videoFile == "" || keyframesJSON == "" {
		http.Error(w, "Missing required form values", http.StatusBadRequest)
		return
	}

	type Keyframe struct {
		Time  float64 `json:"time"`
		CropX int     `json:"cropX"`
	}
	var keyframes []Keyframe
	if err := json.Unmarshal([]byte(keyframesJSON), &keyframes); err != nil {
		http.Error(w, "Invalid keyframes data", http.StatusBadRequest)
		return
	}

	dims, err := getVideoDimensions(filepath.Join("uploads", videoFile))
	if err != nil {
		log.Printf("Failed to get video dimensions: %s", err)
		http.Error(w, "Failed to get video dimensions", http.StatusInternalServerError)
		return
	}

	cropWidth := dims.Height * 9 / 16
	frameRate, err := getVideoFrameRate(filepath.Join("uploads", videoFile))
	if err != nil {
		log.Printf("Failed to get frame rate: %s", err)
		http.Error(w, "Failed to get frame rate", http.StatusInternalServerError)
		return
	}

	// Create zoompan filter with simple linear interpolation
	var zoompanFilter string
	if len(keyframes) > 1 {
		// Use first two keyframes for interpolation
		startX := keyframes[0].CropX - (cropWidth / 2)
		endX := keyframes[1].CropX - (cropWidth / 2)

		// Clamp positions
		if startX < 0 {
			startX = 0
		}
		if endX < 0 {
			endX = 0
		}
		if startX > dims.Width-cropWidth {
			startX = dims.Width - cropWidth
		}
		if endX > dims.Width-cropWidth {
			endX = dims.Width - cropWidth
		}

		// Calculate duration between keyframes
		duration := int((keyframes[1].Time - keyframes[0].Time) * frameRate)
		if duration < 1 {
			duration = 1
		}

		// Simple linear interpolation
		expr := fmt.Sprintf("'%d+(%d-%d)*between(n\\,0\\,%d)*n/%d'",
			startX, endX, startX, duration, duration)
		zoompanFilter = fmt.Sprintf("zoompan=z=1:x=%s:y=0:d=1:s=%dx%d:fps=%f",
			expr, cropWidth, dims.Height, frameRate)
	} else if len(keyframes) == 1 {
		// If only one keyframe, use a fixed position
		xPos := keyframes[0].CropX - (cropWidth / 2)
		if xPos < 0 {
			xPos = 0
		}
		if xPos > dims.Width-cropWidth {
			xPos = dims.Width - cropWidth
		}
		zoompanFilter = fmt.Sprintf("zoompan=z=1:x=%d:y=0:d=1:s=%dx%d:fps=%f", xPos, cropWidth, dims.Height, frameRate)
	} else {
		// No keyframes, use default center position
		defaultX := (dims.Width - cropWidth) / 2
		zoompanFilter = fmt.Sprintf("zoompan=z=1:x=%d:y=0:d=1:s=%dx%d:fps=%f", defaultX, cropWidth, dims.Height, frameRate)
	}

	clipFile := fmt.Sprintf("manualclip-%s-%s-%s", start, end, videoFile)
	clipPath := filepath.Join("static", clipFile)
	subtitlePath := filepath.ToSlash(filepath.Join("uploads", strings.TrimSuffix(videoFile, filepath.Ext(videoFile))+".vtt"))
	vf_string := fmt.Sprintf("%s,scale=1080:1920,setsar=1,subtitles=%s:force_style='Alignment=2\\,FontName=Arial\\,FontSize=18\\,PrimaryColour=&Hffffff\\,BackColor=&H80000000\\,BorderStyle=1\\,Outline=1\\,Shadow=0\\,MarginV=50'", zoompanFilter, strings.ReplaceAll(subtitlePath, "\\", "/"))

	log.Printf("Running ffmpeg with vf: %s", vf_string)
	cmd := exec.Command("ffmpeg", "-y", "-threads", "0", "-ss", start, "-to", end, "-i", filepath.Join("uploads", videoFile), "-vf", vf_string, "-c:a", "aac", "-af", "loudnorm", clipPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error: %s\n%s", err, output)
		outStr := string(output)
		if strings.Contains(outStr, "Parsed_zoompan") || strings.Contains(outStr, "Unknown function") || strings.Contains(outStr, "Failed to configure output pad") || strings.Contains(outStr, "Error reinitializing filters") {
			log.Printf("Zoompan expression failed, trying static crop fallback")
			// Fallback to a safe static crop using first keyframe or center
			var fallbackX int
			if len(keyframes) > 0 {
				fallbackX = keyframes[0].CropX - (cropWidth / 2)
				if fallbackX < 0 {
					fallbackX = 0
				}
				if fallbackX > dims.Width-cropWidth {
					fallbackX = dims.Width - cropWidth
				}
			} else {
				fallbackX = (dims.Width - cropWidth) / 2
			}
			vfFallback := fmt.Sprintf("crop=%d:%d:%d:0,scale=1080:1920,setsar=1,subtitles=%s:force_style='Alignment=2\\,FontName=Arial\\,FontSize=18\\,PrimaryColour=&Hffffff\\,BackColor=&H80000000\\,BorderStyle=1\\,Outline=1\\,Shadow=0\\,MarginV=50'", cropWidth, dims.Height, fallbackX, strings.ReplaceAll(subtitlePath, "\\", "/"))
			log.Printf("Running ffmpeg fallback with vf: %s", vfFallback)
			cmd2 := exec.Command("ffmpeg", "-y", "-threads", "0", "-ss", start, "-to", end, "-i", filepath.Join("uploads", videoFile), "-vf", vfFallback, "-c:a", "aac", "-af", "loudnorm", clipPath)
			output2, err2 := cmd2.CombinedOutput()
			if err2 != nil {
				log.Printf("ffmpeg fallback error: %s\n%s", err2, output2)
				http.Error(w, fmt.Sprintf("Failed to create clip (zoompan and fallback both failed): %s", output2), http.StatusInternalServerError)
				return
			}
			fmt.Fprintf(w, "/static/%s", clipFile)
			return
		}
		// Not a zoompan-related failure
		http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "/static/%s", clipFile)
}

func parseVTTTimestamp(ts string) (float64, error) {
	// 00:00:00.000 or 00:00.000 format
	ts = strings.Replace(ts, ",", ".", 1)
	parts := strings.Split(ts, ":")
	var hours, minutes, seconds float64
	var err error

	if len(parts) == 3 {
		hours, err = strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		minutes, err = strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
		seconds, err = strconv.ParseFloat(parts[2], 64)
		if err != nil {
			return 0, err
		}
	} else if len(parts) == 2 {
		minutes, err = strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return 0, err
		}
		seconds, err = strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return 0, err
		}
	} else {
		// Accept plain seconds (e.g., "5" or "5.2") as a fallback
		seconds, err = strconv.ParseFloat(strings.TrimSpace(ts), 64)
		if err == nil {
			return seconds, nil
		}
		return 0, fmt.Errorf("invalid timestamp format: %s", ts)
	}

	return hours*3600 + minutes*60 + seconds, nil
}

func createZoompanFilter(keyframes []Keyframe, clipStartSec float64, frameRate float64, cropWidth int, dims *videoDimensions) string {
	if len(keyframes) == 1 {
		// If only one keyframe, use a fixed position
		xPos := keyframes[0].CropX - (cropWidth / 2)
		if xPos < 0 {
			xPos = 0
		}
		if xPos > dims.Width-cropWidth {
			xPos = dims.Width - cropWidth
		}
		return fmt.Sprintf("zoompan=z=1:x=%d:y=0:d=1:s=%dx%d:fps=%f", xPos, cropWidth, dims.Height, frameRate)
	} else if len(keyframes) > 1 {
		// Just use the first two keyframes for a simple linear interpolation
		startKf := keyframes[0]
		endKf := keyframes[1]

		// Calculate total duration in frames
		duration := int((endKf.Time - startKf.Time) * frameRate)
		if duration < 1 {
			duration = 1
		}

		// Calculate and clamp x positions
		startX := startKf.CropX - (cropWidth / 2)
		endX := endKf.CropX - (cropWidth / 2)

		if startX < 0 {
			startX = 0
		}
		if endX < 0 {
			endX = 0
		}
		if startX > dims.Width-cropWidth {
			startX = dims.Width - cropWidth
		}
		if endX > dims.Width-cropWidth {
			endX = dims.Width - cropWidth
		}

		// Simple linear interpolation
		return fmt.Sprintf("zoompan=z=1:x='if(gte(n,0),%d+(%d-%d)*min(1,n/%d),%d)':y=0:d=1:s=%dx%d:fps=%f",
			startX, endX, startX, duration, startX, cropWidth, dims.Height, frameRate)
	}

	// Default to center if no keyframes
	defaultX := (dims.Width - cropWidth) / 2
	return fmt.Sprintf("zoompan=z=1:x=%d:y=0:d=1:s=%dx%d:fps=%f", defaultX, cropWidth, dims.Height, frameRate)
}

func parseVTT(vttPath string) ([]TranscriptSegment, error) {
	file, err := os.Open(vttPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var segments []TranscriptSegment
	var currentSegment *TranscriptSegment

	// Skip WEBVTT header
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			break
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "-->") {
			parts := strings.Split(line, " --> ")
			if len(parts) == 2 {
				start, err1 := parseVTTTimestamp(strings.Split(parts[0], " ")[0])
				end, err2 := parseVTTTimestamp(strings.Split(parts[1], " ")[0])
				if err1 == nil && err2 == nil {
					currentSegment = &TranscriptSegment{Start: start, End: end}
				}
			}
		} else if currentSegment != nil && line != "" {
			currentSegment.Text = line
			segments = append(segments, *currentSegment)
			currentSegment = nil // Reset for the next segment
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return segments, nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func transcribeVideo(videoPath string) ([]TranscriptSegment, error) {
	log.Printf("Starting transcription for video: %s", videoPath)

	// 1. Extract audio to a temporary WAV file.
	audioPath := filepath.Join("uploads", "temp_audio.wav")
	cmdAudio := exec.Command("ffmpeg", "-threads", "0", "-i", videoPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "-y", audioPath)
	output, err := cmdAudio.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg audio extraction error: %s\n%s", err, output)
		return nil, fmt.Errorf("failed to extract audio: %s", output)
	}
	defer os.Remove(audioPath)

	// 2. Run whisper.cpp to transcribe the audio and output to a VTT file.
	var whisperCliPath string
	if runtime.GOOS == "windows" {
		defaultPath := filepath.Join(".", "whisper.cpp", "build", "bin", "Release", "whisper-cli.exe")
		whisperCliPath = getEnv("WHISPER_CLI_PATH", defaultPath)
	} else {
		defaultPath := filepath.Join(".", "whisper.cpp", "build", "bin", "whisper-cli")
		whisperCliPath = getEnv("WHISPER_CLI_PATH", defaultPath)
	}

	defaultModelPath := filepath.Join(".", "whisper.cpp", "models", "ggml-base.en.bin")
	whisperModelPath := getEnv("WHISPER_MODEL_PATH", defaultModelPath)
	transcriptOutputPath := filepath.Join("uploads", strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath)))

	// Prepare arguments for whisper-cli
	args := []string{
		"-m", whisperModelPath,
		"-f", audioPath,
		"-ovtt", // Output in VTT format
		"-of", transcriptOutputPath,
	}

	// Check for GPU layers environment variable to enable GPU acceleration
	if gpuLayers, ok := os.LookupEnv("WHISPER_GPU_LAYERS"); ok {
		args = append(args, "--n-gpu-layers", gpuLayers)
		log.Printf("GPU acceleration enabled with %s layers.", gpuLayers)
	}

	cmdWhisper := exec.Command(whisperCliPath, args...)
	cmdWhisper.Dir = "." // Run from the app's root directory

	output, err = cmdWhisper.CombinedOutput()
	if err != nil {
		log.Printf("whisper.cpp error: %s\n%s", err, output)
		return nil, fmt.Errorf("failed to transcribe audio: %s", output)
	}

	// The tool appends .vtt to the output file name.
	transcriptVTTPath := transcriptOutputPath + ".vtt"

	// 3. Read and parse the transcript VTT file.
	transcript, err := parseVTT(transcriptVTTPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VTT transcript: %w", err)
	}

	log.Printf("Transcription successful for video: %s", videoPath)
	return transcript, nil
}

func downloadCascadeFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), os.ModePerm); err != nil {
		return err
	}
	url := "https://github.com/esimov/pigo/raw/master/cascade/facefinder"
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func extractFrames(videoPath, start, end string, fps float64) (string, []string, error) {
	tempDir, err := os.MkdirTemp("", "frames")
	if err != nil {
		return "", nil, err
	}

	// Extract frames
	// -vsync 0 prevents dropping/duplicating frames to match fps, ensuring we get exactly what we ask for
	cmd := exec.Command("ffmpeg", "-y", "-threads", "0", "-ss", start, "-to", end, "-i", videoPath, "-vf", fmt.Sprintf("fps=%f", fps), filepath.Join(tempDir, "frame_%04d.jpg"))
	if output, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tempDir)
		return "", nil, fmt.Errorf("ffmpeg error: %s\n%s", err, output)
	}

	files, err := os.ReadDir(tempDir)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", nil, err
	}

	var paths []string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".jpg") {
			paths = append(paths, filepath.Join(tempDir, f.Name()))
		}
	}
	return tempDir, paths, nil
}

func detectFacesInFrames(framePaths []string, cascadePath string, fps float64, videoWidth int) ([]Keyframe, error) {
	cascadeFile, err := os.ReadFile(cascadePath)
	if err != nil {
		return nil, err
	}

	p := pigo.NewPigo()
	classifier, err := p.Unpack(cascadeFile)
	if err != nil {
		return nil, err
	}

	var keyframes []Keyframe
	var lastX int = -1

	// Max jump allowed in one frame (e.g., 10% of width)
	// At 10fps, moving 10% of screen in 0.1s is very fast.
	maxJump := videoWidth / 10

	for i, path := range framePaths {
		src, err := pigo.GetImage(path)
		if err != nil {
			continue
		}

		pixels := pigo.RgbToGrayscale(src)
		cols, rows := src.Bounds().Max.X, src.Bounds().Max.Y
		cParams := pigo.CascadeParams{
			MinSize:     20,
			MaxSize:     1000,
			ShiftFactor: 0.1,
			ScaleFactor: 1.1,
			ImageParams: pigo.ImageParams{Pixels: pixels, Rows: rows, Cols: cols, Dim: cols},
		}

		detections := classifier.RunCascade(cParams, 0.0)
		detections = classifier.ClusterDetections(detections, 0.2)

		var bestX int = -1

		if len(detections) > 0 {
			log.Printf("Frame %d: Detected %d faces", i, len(detections))
			if lastX == -1 {
				// First detection: pick largest face
				maxSize := 0
				for _, d := range detections {
					if d.Scale > maxSize {
						maxSize = d.Scale
						bestX = d.Col
					}
				}
			} else {
				// Subsequent: pick closest to lastX
				minDist := 999999
				for _, d := range detections {
					dist := int(math.Abs(float64(d.Col - lastX)))
					if dist < minDist {
						minDist = dist
						bestX = d.Col
					}
				}

				// Sanity check: did we jump too far?
				if bestX != -1 {
					dist := int(math.Abs(float64(bestX - lastX)))
					if dist > maxJump {
						log.Printf("Frame %d: Ignored jump of %d pixels (max %d)", i, dist, maxJump)
						bestX = -1 // Ignore this detection
					}
				}
			}
		} else {
			log.Printf("Frame %d: No faces detected", i)
		}

		if bestX != -1 {
			lastX = bestX
		} else if lastX != -1 {
			// Keep last known position
			bestX = lastX
		} else {
			// No face ever found yet, default to center
			bestX = videoWidth / 2
		}

		keyframes = append(keyframes, Keyframe{
			Time:  float64(i) / fps,
			CropX: bestX,
		})
	}

	return keyframes, nil
}

func smoothPath(keyframes []Keyframe, videoWidth, cropWidth int) []Keyframe {
	if len(keyframes) == 0 {
		return nil
	}

	smoothed := make([]Keyframe, len(keyframes))
	// Window size of 20 at 10fps = 2 seconds smoothing.
	// This provides a very stable, cinematic feel and absorbs small jitters.
	windowSize := 20

	for i := 0; i < len(keyframes); i++ {
		sum := 0
		count := 0

		start := i - windowSize/2
		end := i + windowSize/2

		for j := start; j <= end; j++ {
			if j >= 0 && j < len(keyframes) {
				sum += keyframes[j].CropX
				count++
			}
		}

		avgX := sum / count

		// Clamp
		finalX := avgX - (cropWidth / 2)
		if finalX < 0 {
			finalX = 0
		}
		if finalX+cropWidth > videoWidth {
			finalX = videoWidth - cropWidth
		}

		smoothed[i] = Keyframe{
			Time:  keyframes[i].Time,
			CropX: finalX,
		}
	}
	return smoothed
}

func buildCropExpression(keyframes []Keyframe, fps float64) string {
	if len(keyframes) == 0 {
		return "0"
	}
	if len(keyframes) == 1 {
		return fmt.Sprintf("%d", keyframes[0].CropX)
	}

	var parts []string

	// We use 'lerp' for smooth transitions between keyframes
	// expression: if(between(t, t0, t1), lerp(x0, x1, (t-t0)/(t1-t0)), ...)

	for i := 0; i < len(keyframes)-1; i++ {
		k1 := keyframes[i]
		k2 := keyframes[i+1]

		// Avoid division by zero
		duration := k2.Time - k1.Time
		if duration <= 0.001 {
			continue
		}

		// lerp(A, B, T) = A + (B-A)*T
		// T = (t - k1.Time) / duration

		// We use 'between(t, start, end)' to activate this segment
		// Note: 't' in ffmpeg crop filter is timestamp in seconds
		// IMPORTANT: Escape commas for ffmpeg filter graph

		// segment := fmt.Sprintf("between(t\\,%.3f\\,%.3f)*(%d+(%d-%d)*(t-%.3f)/%.3f)",
		// 	k1.Time, k2.Time,
		// 	k1.CropX, k2.CropX, k1.CropX,
		// 	k1.Time, duration)

		// FIX: 'between' is inclusive, so at the boundary frame, TWO segments are active,
		// summing their values and causing a massive jump.
		// We use gte(t, start) * lt(t, end) to make it exclusive.
		segment := fmt.Sprintf("gte(t\\,%.3f)*lt(t\\,%.3f)*(%d+(%d-%d)*(t-%.3f)/%.3f)",
			k1.Time, k2.Time,
			k1.CropX, k2.CropX, k1.CropX,
			k1.Time, duration)

		parts = append(parts, segment)
	}

	// Handle time after last keyframe (hold last position)
	lastK := keyframes[len(keyframes)-1]
	parts = append(parts, fmt.Sprintf("gte(t\\,%.3f)*%d", lastK.Time, lastK.CropX))

	return strings.Join(parts, "+")
}
