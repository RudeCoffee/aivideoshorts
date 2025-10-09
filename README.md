# Video Clipper

This is a web application that allows a user to upload a video, receive a transcript of that video, and then create shorter video clips by selecting text from the transcript.

## Features

-   Upload video files through a web interface.
-   Automatic transcription of the video's audio using a local `whisper.cpp` instance.
-   Interactive transcript display where users can select segments.
-   Video clip generation based on selected transcript segments using `ffmpeg`.

## Setup and Installation

### Prerequisites

-   **Go:** You need to have the Go programming language installed.
-   **ffmpeg:** This application uses `ffmpeg` for video and audio processing. Make sure it is installed and available in your system's PATH.
-   **C++ Compiler:** A C++ compiler (like `g++`) is required to build the `whisper.cpp` library.

### 1. Clone the Application Repository

First, clone this repository to your local machine:

```bash
git clone <repository-url>
cd <repository-directory>
```

### 2. Set Up the Transcription Service

This application uses `whisper.cpp` for local transcription. You need to clone its repository and prepare the model.

```bash
# Clone the whisper.cpp repository
git clone https://github.com/ggerganov/whisper.cpp.git

# Build the whisper.cpp command-line tool
cd whisper.cpp
make
cd ..

# Download a pre-trained Whisper model
# This example uses the base English model, but you can choose others.
./whisper.cpp/models/download-ggml-model.sh base.en
```

### 3. Run the Application

Once the setup is complete, you can build and run the web server:

```bash
go build -o video-clipper-app .
./video-clipper-app
```

The server will start on `http://localhost:8080`.

### 4. Configuration (Optional)

The application can be configured using environment variables if you have placed the `whisper.cpp` files in a different location:

-   `WHISPER_CLI_PATH`: The path to the `whisper-cli` executable.
    -   Default: `./whisper.cpp/build/bin/whisper-cli`
-   `WHISPER_MODEL_PATH`: The path to the `ggml` model file.
    -   Default: `./whisper.cpp/models/ggml-base.en.bin`

Example:
```bash
export WHISPER_CLI_PATH="/path/to/your/whisper-cli"
export WHISPER_MODEL_PATH="/path/to/your/model.bin"
./video-clipper-app
```