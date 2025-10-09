# Video Clipper

This is a web application that allows a user to upload a video, receive a transcript of that video, and then create shorter video clips by selecting text from the transcript.

## Local Machine Setup

Follow these instructions to run the application locally on your machine.

### Prerequisites

-   **Go:** You need to have the Go programming language installed.
-   **ffmpeg:** You need to have `ffmpeg` (`ffmpeg.exe` on Windows) installed and its location added to your system's `PATH`.
-   **C++ Compiler:** You will need a C++ compiler (like `g++` or the one in Visual Studio) to build `whisper.cpp`.

### 1. Clone the Application Repository

First, clone this repository to your local machine:

```bash
git clone <repository-url>
cd <repository-directory>
```

### 2. Set Up the Transcription Service

This application uses `whisper.cpp` for local transcription. You need to clone its repository and build it.

```bash
# Clone the whisper.cpp repository (it should be inside the main project folder)
git clone https://github.com/ggerganov/whisper.cpp.git

# Build the whisper.cpp command-line tool
cd whisper.cpp
make # On Linux/macOS
# On Windows, follow the whisper.cpp instructions, which usually involves CMake and Visual Studio.
cd ..

# Download a pre-trained Whisper model
./whisper.cpp/models/download-ggml-model.sh base.en
```

### 3. Run the Application

The application is now platform-aware and should work out-of-the-box if you followed the standard setup.

```bash
# Build and run the Go application
go build -o video-clipper-app .
./video-clipper-app
```

The server will start on `http://localhost:8080`.

### 4. Configuration (Optional)

The application automatically looks for the `whisper-cli` executable in the standard build directories. If your setup is different, you can use environment variables to point the application to the correct paths.

**Default Paths:**
-   **Linux/macOS:** `./whisper.cpp/build/bin/whisper-cli`
-   **Windows:** `./whisper.cpp/build/bin/Release/whisper-cli.exe`
-   **Model (all platforms):** `./whisper.cpp/models/ggml-base.en.bin`

If your paths are different, set the following environment variables before running the application:

**PowerShell (Windows):**
```powershell
$env:WHISPER_CLI_PATH="C:\path\to\your\whisper-cli.exe"
$env:WHISPER_MODEL_PATH="C:\path\to\your\ggml-base.en.bin"
```

**Bash (Linux/macOS):**
```bash
export WHISPER_CLI_PATH="/path/to/your/whisper-cli"
export WHISPER_MODEL_PATH="/path/to/your/ggml-base.en.bin"
```

---

## GPU Acceleration (NVIDIA)

If you have an NVIDIA graphics card, you can significantly speed up the transcription process by compiling `whisper.cpp` with CUDA support and offloading the work to your GPU.

### Prerequisites for GPU Acceleration

-   **NVIDIA Drivers:** You must have the appropriate NVIDIA drivers installed for your GPU.
-   **CUDA Toolkit:** You need to have the NVIDIA CUDA Toolkit installed.

### 1. Compile with CUDA Support

When you build `whisper.cpp`, use the following command to enable CUDA support:

```bash
# Inside the whisper.cpp directory
make whisper-cli GGML_CUDA=1
```

This will create the `whisper-cli` executable with GPU capabilities.

### 2. Run with GPU Acceleration

To enable GPU acceleration, set the `WHISPER_GPU_LAYERS` environment variable before running the application. This variable tells `whisper.cpp` how many of the model's layers to offload to the GPU. A good starting point is a non-zero value; you can experiment to find the best performance for your card.

**PowerShell (Windows):**
```powershell
# A value greater than 0 enables GPU offloading.
$env:WHISPER_GPU_LAYERS="1"
./video-clipper-app.exe
```

**Bash (Linux/macOS):**
```bash
# A value greater than 0 enables GPU offloading.
export WHISPER_GPU_LAYERS=1
./video-clipper-app
```