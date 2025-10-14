# Testing Instructions for Video Clipper

This document outlines the steps to set up the environment and run tests for the Video Clipper application.

## 1. Environment Setup

Before running the application for the first time, please ensure the following dependencies are installed.

### a. Install ffmpeg

**For Debian/Ubuntu-based systems:**
```bash
sudo apt-get update && sudo apt-get install -y ffmpeg
```

**For other systems:**
Please use your system's package manager to install `ffmpeg`.

### b. Install Go

The application backend is written in Go. Please follow the official instructions to install Go: [https://golang.org/doc/install](https://golang.org/doc/install)

### c. Install Python and Playwright

The verification script uses Python and Playwright.

1.  **Install Python 3 and pip.**
2.  **Install Playwright and its browser dependencies:**
    ```bash
    pip install playwright
    playwright install
    ```

### d. Prepare a Test Video

Place a sample video file named `test.mp4` in the root of the repository. This will be used for the upload test.

## 2. Running the Application and Tests

Once the environment is set up, follow these steps to run the verification script.

### a. Start the Application Server

In your terminal, from the root of the repository, run:
```bash
go run main.go &
```
This command starts the server in the background. It will be accessible at `http://localhost:8080`.

### b. Run the Verification Script

The following Python script will be used to test the application's frontend. I will create it as `jules-scratch/verification/verify.py`.

```python
from playwright.sync_api import sync_playwright, expect
import os

def run(playwright):
    browser = playwright.chromium.launch(headless=True)
    page = browser.new_page()

    try:
        # 1. Go to the app
        print("Navigating to the application...")
        page.goto("http://localhost:8080", timeout=20000) # Increased timeout for server to start

        # 2. Upload a video
        print("Uploading test video...")
        # Assumes a file named 'test.mp4' exists in the repository root
        if not os.path.exists("test.mp4"):
            print("Error: test.mp4 not found. Please add a sample video to the root directory.")
            return

        page.set_input_files("input[name=video]", "test.mp4")
        page.get_by_role("button", name="Upload and Transcribe").click()

        # 3. Wait for transcription and select a segment
        print("Waiting for transcription to complete...")
        # Wait for the first transcript paragraph to appear (up to 2 minutes)
        first_segment = page.locator("#transcript p").first
        expect(first_segment).to_be_visible(timeout=120000)
        print("Transcription complete. Selecting segments...")

        # Click the first two segments
        first_segment.click()
        page.locator("#transcript p").nth(1).click()

        # 4. Verify buttons are enabled
        print("Verifying that clipping buttons are enabled...")
        create_clip_button = page.get_by_role("button", name="Create Clip")
        autoclip_button = page.get_by_role("button", name="Auto-Clip for Shorts")
        expect(create_clip_button).to_be_enabled()
        expect(autoclip_button).to_be_enabled()
        print("Buttons are enabled as expected.")

        # 5. Take a screenshot of the enabled state
        screenshot_path = "jules-scratch/verification/verification.png"
        page.screenshot(path=screenshot_path)
        print(f"Successfully took screenshot: {screenshot_path}")

    except Exception as e:
        print(f"An error occurred during verification: {e}")
        page.screenshot(path="jules-scratch/verification/error.png")
    finally:
        browser.close()
        print("Verification script finished.")

with sync_playwright() as playwright:
    run(playwright)
```

### c. Execute the script

I will run the script using:
```bash
python jules-scratch/verification/verify.py
```
This will perform the test and save a screenshot for visual confirmation.