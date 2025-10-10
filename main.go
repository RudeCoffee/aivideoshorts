package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // to decode jpeg images
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	pigo "github.com/esimov/pigo/core"
)

var classifier *pigo.Pigo

// TranscriptSegment represents a single segment of the video transcript.
type TranscriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// UploadResponse is the structure of the JSON response sent after a video upload.
type UploadResponse struct {
	Transcript  []TranscriptSegment `json:"transcript"`
	VideoFile   string              `json:"videoFile"`
}

func main() {
	// Check if ffmpeg is installed.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Fatal("ffmpeg is not installed or not in the system's PATH.")
	}

	// Load the face detection classifier
	cascadeFile, err := ioutil.ReadFile("facefinder")
	if err != nil {
		log.Fatalf("Error reading cascade file: %v", err)
	}

	p := pigo.NewPigo()
	// Unpack the binary file. This will return the number of cascade trees,
	// the tree depth, the starting points of the tree nodes, and the point group size.
	classifier, err = p.Unpack(cascadeFile)
	if err != nil {
		log.Fatalf("Error unpacking cascade file: %v", err)
	}

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "templates/index.html")
	})
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/clip", clipHandler)
	http.HandleFunc("/autoclip", autoClipHandler)

	log.Println("Starting server on :8080")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Received new upload request.")
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
	log.Printf("File %s uploaded successfully.", handler.Filename)

	log.Printf("Starting transcription for %s", dst.Name())
	transcript, err := transcribeVideo(dst.Name())
	if err != nil {
		log.Printf("Transcription failed for %s: %v", dst.Name(), err)
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

	cmd := exec.Command("ffmpeg", "-i", filepath.Join("uploads", videoFile), "-ss", start, "-to", end, "-c", "copy", clipPath)
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

	if start == "" || end == "" || videoFile == "" {
		http.Error(w, "Missing required form values: start, end, and videoFile", http.StatusBadRequest)
		return
	}

	inputVideoPath := filepath.Join("uploads", videoFile)

	faceXPositions, videoWidth, videoHeight, err := detectFaces(inputVideoPath, start, end)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to detect faces: %v", err), http.StatusInternalServerError)
		return
	}

	clipFile := fmt.Sprintf("autoclip-%s-%s-%s", start, end, videoFile)
	clipPath := filepath.Join("static", clipFile)

	var filter string
	if len(faceXPositions) == 0 {
		log.Println("No faces detected, centering video.")
		filter = "crop=ih*9/16:ih,scale=1080:1920,setsar=1"
	} else {
		var frames []int
		for f := range faceXPositions {
			frames = append(frames, f)
		}
		sort.Ints(frames)

		// initial position is center
		initialX := (videoWidth - (videoHeight * 9 / 16)) / 2
		if len(frames) > 0 {
			// start with the position of the first detected face
			faceX := faceXPositions[frames[0]]
			cropWidth := videoHeight * 9 / 16
			targetX := faceX - (cropWidth / 2)
			if targetX < 0 {
				targetX = 0
			}
			if targetX+cropWidth > videoWidth {
				targetX = videoWidth - cropWidth
			}
			initialX = targetX
		}

		var expr strings.Builder

		// build nested if expression from last to first frame
		for i := len(frames) - 1; i >= 0; i-- {
			frameNum := frames[i]
			faceX := faceXPositions[frameNum]
			cropWidth := videoHeight * 9 / 16
			targetX := faceX - (cropWidth / 2)

			if targetX < 0 {
				targetX = 0
			}
			if targetX+cropWidth > videoWidth {
				targetX = videoWidth - cropWidth
			}

			t := float64(frameNum-1) / 5.0

			if expr.Len() == 0 {
				expr.WriteString(fmt.Sprintf("if(gte(t,%.4f),%d,%d)", t, targetX, initialX))
			} else {
				expr.WriteString(fmt.Sprintf("if(gte(t,%.4f),%d,%s)", t, targetX, expr.String()))
			}
		}

		if expr.Len() == 0 {
			// No faces, center it.
			filter = "crop=ih*9/16:ih,scale=1080:1920,setsar=1"
		} else {
			filter = fmt.Sprintf("crop=w=ih*9/16:h=ih:x='%s',scale=1080:1920,setsar=1", expr.String())
		}
		log.Printf("Using filter: %s", filter)
	}

	// Command to crop to 9:16, scale, and maintain aspect ratio
	cmd := exec.Command("ffmpeg", "-i", inputVideoPath, "-vf", filter, "-ss", start, "-to", end, "-c:a", "aac", "-b:a", "192k", clipPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg error: %s\n%s", err, output)
		http.Error(w, fmt.Sprintf("Failed to create clip: %s", output), http.StatusInternalServerError)
		return
	}

	fmt.Fprintf(w, "/static/%s", clipFile)
}

func detectFaces(videoPath, start, end string) (map[int]int, int, int, error) {
	tempDir, err := ioutil.TempDir("", "frames")
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Get video dimensions
	var width, height int
	cmdProbe := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=s=x:p=0", videoPath)
	output, err := cmdProbe.CombinedOutput()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("ffprobe failed: %s, %w", output, err)
	}
	parts := strings.Split(strings.TrimSpace(string(output)), "x")
	if len(parts) == 2 {
		width, _ = strconv.Atoi(parts[0])
		height, _ = strconv.Atoi(parts[1])
	}
	if width == 0 || height == 0 {
		return nil, 0, 0, fmt.Errorf("could not get video dimensions")
	}

	// Extract frames
	cmd := exec.Command("ffmpeg", "-i", videoPath, "-ss", start, "-to", end, "-vf", "fps=5", filepath.Join(tempDir, "frame-%04d.jpg"))
	output, err = cmd.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg frame extraction error: %s\n%s", err, output)
		// This might not be a fatal error, maybe some frames were extracted
	}

	files, err := ioutil.ReadDir(tempDir)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("failed to read temp dir: %w", err)
	}

	faceXPositions := make(map[int]int)

	for _, fileInfo := range files {
		if !strings.HasSuffix(fileInfo.Name(), ".jpg") {
			continue
		}

		frameNum, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(fileInfo.Name(), "frame-"), ".jpg"))
		if err != nil {
			continue
		}

		imgPath := filepath.Join(tempDir, fileInfo.Name())
		file, err := os.Open(imgPath)
		if err != nil {
			log.Printf("failed to open image %s: %v", imgPath, err)
			continue
		}

		src, _, err := image.Decode(file)
		file.Close()
		if err != nil {
			log.Printf("failed to decode image %s: %v", imgPath, err)
			continue
		}

		pixels := pigo.RgbToGrayscale(src)
		cols, rows := src.Bounds().Max.X, src.Bounds().Max.Y

		cParams := pigo.CascadeParams{
			MinSize:     20,
			MaxSize:     1000,
			ShiftFactor: 0.1,
			ScaleFactor: 1.1,
			ImageParams: pigo.ImageParams{
				Pixels: pixels,
				Rows:   rows,
				Cols:   cols,
				Dim:    cols,
			},
		}

		dets := classifier.RunCascade(cParams, 0.0)
		dets = classifier.ClusterDetections(dets, 0.2)

		if len(dets) > 0 {
			// Find the most confident detection
			sort.Slice(dets, func(i, j int) bool {
				return dets[i].Q > dets[j].Q
			})
			faceXPositions[frameNum] = dets[0].Col
		}
	}

	return faceXPositions, width, height, nil
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
	log.Printf("Running ffmpeg command: %s", cmdAudio.String())
	output, err := cmdAudio.CombinedOutput()
	if err != nil {
		log.Printf("ffmpeg audio extraction error: %s\n%s", err, output)
		return nil, fmt.Errorf("failed to extract audio: %s", output)
	}
	log.Printf("ffmpeg audio extraction successful for %s.", videoPath)
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
	transcriptOutputPath := filepath.Join("uploads", "temp_transcript")

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
	log.Printf("Running whisper.cpp command: %s %v", whisperCliPath, args)

	output, err = cmdWhisper.CombinedOutput()
	if err != nil {
		log.Printf("whisper.cpp error: %s\n%s", err, output)
		return nil, fmt.Errorf("failed to transcribe audio: %s", output)
	}
	log.Printf("whisper.cpp transcription successful for %s.", audioPath)

	// The tool appends .vtt to the output file name.
	transcriptVTTPath := transcriptOutputPath + ".vtt"
	log.Printf("VTT file generated at: %s", transcriptVTTPath)
	defer os.Remove(transcriptVTTPath)

	// 3. Read and parse the transcript VTT file.
	log.Printf("Parsing VTT file: %s", transcriptVTTPath)
	transcript, err := parseVTT(transcriptVTTPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VTT transcript: %w", err)
	}

	log.Printf("Transcription successful for video: %s", videoPath)
	return transcript, nil
}