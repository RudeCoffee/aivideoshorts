document.addEventListener('DOMContentLoaded', () => {
    const uploadForm = document.getElementById('upload-form');
    const uploadButton = uploadForm.querySelector('button');
    const transcriptDiv = document.getElementById('transcript');
    const clipForm = document.getElementById('clip-form');
    const clipResultDiv = document.getElementById('clip-result');
    const cropModal = document.getElementById('crop-modal');
    const cropImage = document.getElementById('crop-image');
    const confirmCropButton = document.getElementById('confirm-crop-button');
    let videoFile = '';
    let cropper;
    let cropData;

    uploadForm.addEventListener('submit', async (e) => {
        e.preventDefault();

        uploadButton.disabled = true;
        uploadButton.textContent = 'Transcribing...';
        transcriptDiv.innerHTML = '<p>Transcription in progress. This may take a few minutes for longer videos...</p>';
        clipResultDiv.innerHTML = '';

        const formData = new FormData(uploadForm);

        try {
            const response = await fetch('/upload', {
                method: 'POST',
                body: formData,
            });

            if (response.ok) {
                const data = await response.json();
                videoFile = data.videoFile;
                const transcript = data.transcript;
                transcriptDiv.innerHTML = ''; // Clear previous transcript
                if (transcript && transcript.length > 0) {
                    transcript.forEach(segment => {
                        const p = document.createElement('p');
                        p.textContent = segment.text;
                        p.dataset.start = segment.start;
                        p.dataset.end = segment.end;
                        p.addEventListener('click', () => {
                            p.classList.toggle('selected');
                            updateClipTimes();
                        });
                        transcriptDiv.appendChild(p);
                    });
                } else {
                    transcriptDiv.innerHTML = '<p>No speech detected in the video, or the transcript was empty.</p>';
                }
            } else {
                const errorText = await response.text();
                transcriptDiv.innerHTML = `<p><strong>Error during transcription:</strong> ${errorText}</p>`;
            }
        } catch (error) {
            transcriptDiv.innerHTML = `<p><strong>An unexpected error occurred:</strong> ${error.message}</p>`;
        } finally {
            uploadButton.disabled = false;
            uploadButton.textContent = 'Upload and Transcribe';
        }
    });

    const clipButton = clipForm.querySelector('button[type="submit"]');
    const autoClipButton = document.getElementById('autoclip-button');

    const handleClipRequest = async (url, button, cropData) => {
        button.disabled = true;
        button.textContent = 'Creating...';
        clipResultDiv.innerHTML = `<p>Creating clip... This can take a moment.</p>`;

        const formData = new FormData(clipForm);
        formData.append('videoFile', videoFile);
        if (cropData) {
            formData.append('cropX', cropData.x);
            formData.append('cropY', cropData.y);
            formData.append('cropWidth', cropData.width);
            formData.append('cropHeight', cropData.height);
        }

        try {
            const response = await fetch(url, {
                method: 'POST',
                body: new URLSearchParams(formData),
            });

            if (response.ok) {
                const clipPath = await response.text();
                clipResultDiv.innerHTML = `
                    <p>Clip created successfully!</p>
                    <video controls width="100%">
                        <source src="${clipPath}" type="video/mp4">
                        Your browser does not support the video tag.
                    </video>
                    <a href="${clipPath}" download>Download Clip</a>
                `;
            } else {
                const errorText = await response.text();
                clipResultDiv.innerHTML = `<p><strong>Error creating clip:</strong> ${errorText}</p>`;
            }
        } catch (error) {
            clipResultDiv.innerHTML = `<p><strong>An unexpected error occurred:</strong> ${error.message}</p>`;
        } finally {
            button.disabled = false;
            if(button === clipButton) button.textContent = 'Create Clip';
            if(button === autoClipButton) button.textContent = 'Auto-Clip for Shorts';
        }
    };

    clipForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        const start = document.getElementById('start').value;
        if (!start) {
            alert('Please select a start time from the transcript.');
            return;
        }

        // Show loading indicator
        clipResultDiv.innerHTML = '<p>Extracting frame for cropping...</p>';

        try {
            // 1. Request the first frame for cropping
            const frameResponse = await fetch('/extract-frame', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams({
                    videoFile: videoFile,
                    start: start,
                }),
            });

            if (!frameResponse.ok) {
                const errorText = await frameResponse.text();
                clipResultDiv.innerHTML = `<p><strong>Error extracting frame:</strong> ${errorText}</p>`;
                return;
            }

            const framePath = await frameResponse.text();

            // 2. Show the modal and initialize Cropper.js
            cropImage.src = framePath;
            cropModal.style.display = 'block';
            if (cropper) {
                cropper.destroy();
            }
            cropper = new Cropper(cropImage, {
                aspectRatio: 9 / 16,
                viewMode: 1,
                autoCropArea: 0.8
            });

        } catch (error) {
            clipResultDiv.innerHTML = `<p><strong>An unexpected error occurred while extracting the frame:</strong> ${error.message}</p>`;
        }
    });

    confirmCropButton.addEventListener('click', () => {
        if (cropper) {
            cropData = cropper.getData(true); // Get rounded crop data
            cropModal.style.display = 'none';
            cropper.destroy();
            handleClipRequest('/clip', clipButton, cropData);
        }
    });

    function updateClipTimes() {
        const selected = transcriptDiv.querySelectorAll('p.selected');
        if (selected.length > 0) {
            let minStart = selected[0].dataset.start;
            let maxEnd = selected[0].dataset.end;
            selected.forEach(p => {
                if (p.dataset.start < minStart) {
                    minStart = p.dataset.start;
                }
                if (p.dataset.end > maxEnd) {
                    maxEnd = p.dataset.end;
                }
            });
            document.getElementById('start').value = minStart;
            document.getElementById('end').value = maxEnd;
        } else {
            document.getElementById('start').value = '';
            document.getElementById('end').value = '';
        }
    }

    autoClipButton.addEventListener('click', () => {
        handleClipRequest('/autoclip', autoClipButton);
    });
});