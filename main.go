package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	_ "image"
	_ "image/png"
	"io"
	"log"
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

type FaceDetection struct {
	Frame int
	X     int
}

func smoothFaceDetections(detections []FaceDetection, windowSize int) []FaceDetection {
	if windowSize <= 1 {
		return detections
	}

	var smoothedDetections []FaceDetection
	for i := range detections {
		start := i - windowSize/2
		if start < 0 {
			start = 0
		}
		end := i + windowSize/2
		if end >= len(detections) {
			end = len(detections) - 1
		}

		sum := 0
		count := 0
		for j := start; j <= end; j++ {
			sum += detections[j].X
			count++
		}
		smoothedX := sum / count
		smoothedDetections = append(smoothedDetections, FaceDetection{Frame: detections[i].Frame, X: smoothedX})
	}
	return smoothedDetections
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

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "templates/index.html")
	})
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/clip", clipHandler)
	http.HandleFunc("/autoclip", autoClipHandler)
	http.HandleFunc("/first-frame", firstFrameHandler)

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

	if cropXStr != "" {
		// Manual crop logic
		manualCropX, err := strconv.Atoi(cropXStr)
		if err != nil {
			http.Error(w, "Invalid cropX value", http.StatusBadRequest)
			return
		}
		cropX := manualCropX - (cropWidth / 2)
		if cropX < 0 {
			cropX = 0
		}
		if cropX+cropWidth > dims.Width {
			cropX = dims.Width - cropWidth
		}

		subtitlePath := filepath.ToSlash(filepath.Join("uploads", strings.TrimSuffix(videoFile, filepath.Ext(videoFile))+".vtt"))
		vf_string := fmt.Sprintf("crop=%d:%d:%d:0,scale=1080:1920,setsar=1,subtitles=%s:force_style='Alignment=2\\,FontName=Arial\\,FontSize=18\\,PrimaryColour=&Hffffff\\,BackColor=&H80000000\\,BorderStyle=1\\,Outline=1\\,Shadow=0\\,MarginV=50'", cropWidth, dims.Height, cropX, strings.ReplaceAll(subtitlePath, "\\", "/"))
		cmd := exec.Command("ffmpeg", "-y", "-ss", start, "-to", end, "-i", filepath.Join("uploads", videoFile), "-vf", vf_string, "-c:a", "aac", "-af", "loudnorm", clipPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("ffmpeg error: %s\n%s", err, output)
			http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
			return
		}
	} else {
		// Face tracking logic
		frameDir := filepath.Join("uploads", strings.TrimSuffix(clipFile, filepath.Ext(videoFile))+"_frames")
		if err := os.MkdirAll(frameDir, os.ModePerm); err != nil {
			http.Error(w, "Failed to create frame directory", http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(frameDir)

		framePattern := filepath.Join(frameDir, "frame-%04d.png")
		cmdFrame := exec.Command("ffmpeg", "-y", "-ss", start, "-to", end, "-i", filepath.Join("uploads", videoFile), framePattern)
		output, err := cmdFrame.CombinedOutput()
		if err != nil {
			log.Printf("ffmpeg error extracting frames: %s\n%s", err, output)
			http.Error(w, fmt.Sprintf("Failed to extract frames: %s", output), http.StatusInternalServerError)
			return
		}

		files, err := os.ReadDir(frameDir)
		if err != nil {
			log.Printf("Failed to read frame directory: %s", err)
			http.Error(w, "Failed to read frame directory", http.StatusInternalServerError)
			return
		}

		p := pigo.NewPigo()
		cascadeFile, err := os.ReadFile(filepath.Join("cascade", "facefinder"))
		if err != nil {
			log.Printf("Failed to read cascade file: %s", err)
			http.Error(w, "Failed to read cascade file", http.StatusInternalServerError)
			return
		}
		classifier, err := p.Unpack(cascadeFile)
		if err != nil {
			log.Printf("Error unpacking cascade file: %s", err)
			http.Error(w, "Error unpacking cascade file", http.StatusInternalServerError)
			return
		}

		var faceDetections []FaceDetection
		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".png") {
				framePath := filepath.Join(frameDir, file.Name())
				// Extract frame number from filename
				parts := strings.Split(file.Name(), "-")
				if len(parts) != 2 {
					continue
				}
				frameNumStr := strings.TrimSuffix(parts[1], ".png")
				frameNum, err := strconv.Atoi(frameNumStr)
				if err != nil {
					continue
				}

				src, err := pigo.GetImage(filepath.ToSlash(framePath))
				if err != nil {
					log.Printf("Cannot open image file %s: %v", framePath, err)
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
				if len(detections) > 0 {
					faceDetections = append(faceDetections, FaceDetection{Frame: frameNum, X: detections[0].Col})
				}
			}
		}

		// Smooth face detections
		smoothedDetections := smoothFaceDetections(faceDetections, 15)

		type CropKeyframe struct {
			Frame int
			CropX int
		}
		var keyframes []CropKeyframe
		safeZoneThreshold := int(float64(cropWidth) * 0.15)

		if len(smoothedDetections) > 0 {
			currentCropX := smoothedDetections[0].X - (cropWidth / 2)
			keyframes = append(keyframes, CropKeyframe{Frame: smoothedDetections[0].Frame, CropX: currentCropX})

			for _, detection := range smoothedDetections {
				cropCenterX := currentCropX + (cropWidth / 2)
				delta := detection.X - cropCenterX

				if delta > safeZoneThreshold || delta < -safeZoneThreshold {
					newCropX := detection.X - (cropWidth / 2)
					keyframes = append(keyframes, CropKeyframe{Frame: detection.Frame, CropX: newCropX})
					currentCropX = newCropX
				}
			}
		}

		log.Printf("Calculated keyframes: %+v", keyframes)

		frameRate, err := getVideoFrameRate(filepath.Join("uploads", videoFile))
		if err != nil {
			log.Printf("Failed to get frame rate: %s", err)
			http.Error(w, "Failed to get frame rate", http.StatusInternalServerError)
			return
		}

		var zoompanExpressions []string
		for i := 0; i < len(keyframes)-1; i++ {
			startFrame := keyframes[i].Frame
			endFrame := keyframes[i+1].Frame
			startX := keyframes[i].CropX
			endX := keyframes[i+1].CropX
			expr := fmt.Sprintf("if(between(in_frame,%d,%d),lerp(%d,%d,(in_frame-%d)/(%d-%d)),", startFrame, endFrame, startX, endX, startFrame, endFrame, startFrame)
			zoompanExpressions = append(zoompanExpressions, expr)
		}

		var xExpr string
		if len(zoompanExpressions) > 0 {
			xExpr = strings.Join(zoompanExpressions, "") + fmt.Sprintf("%d", keyframes[len(keyframes)-1].CropX) + strings.Repeat(")", len(zoompanExpressions))
		} else if len(keyframes) == 1 {
			xExpr = fmt.Sprintf("%d", keyframes[0].CropX)
		} else {
			xExpr = fmt.Sprintf("%d", (dims.Width-cropWidth)/2)
		}

		zoompanFilter := fmt.Sprintf("zoompan=z=1:x='%s':y=0:d=1:s=%dx%d:fps=%f", xExpr, cropWidth, dims.Height, frameRate)

		subtitlePath := filepath.ToSlash(filepath.Join("uploads", strings.TrimSuffix(videoFile, filepath.Ext(videoFile))+".vtt"))
		vf_string := fmt.Sprintf("%s,scale=1080:1920,setsar=1,subtitles=%s:force_style='Alignment=2\\,FontName=Arial\\,FontSize=18\\,PrimaryColour=&Hffffff\\,BackColor=&H80000000\\,BorderStyle=1\\,Outline=1\\,Shadow=0\\,MarginV=50'", zoompanFilter, strings.ReplaceAll(subtitlePath, "\\", "/"))
		cmd := exec.Command("ffmpeg", "-y", "-ss", start, "-to", end, "-i", filepath.Join("uploads", videoFile), "-vf", vf_string, "-c:a", "aac", "-af", "loudnorm", clipPath)
		output, err = cmd.CombinedOutput()
		if err != nil {
			log.Printf("ffmpeg error: %s\n%s", err, output)
			http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
			return
		}
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
	cmd := exec.Command("ffmpeg", "-y", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-vframes", "1", framePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error extracting first frame: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to extract first frame: %s", output), http.StatusInternalServerError)
		return
	}

	// Return the path to the frame so the frontend can display it
	fmt.Fprintf(w, "/%s", filepath.ToSlash(framePath))
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
		return 0, fmt.Errorf("invalid timestamp format: %s", ts)
	}

	return hours*3600 + minutes*60 + seconds, nil
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
	cmdAudio := exec.Command("ffmpeg", "-i", videoPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "-y", audioPath)
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
