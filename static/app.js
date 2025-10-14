document.addEventListener('DOMContentLoaded', () => {
    const uploadForm = document.getElementById('upload-form');
    const uploadButton = uploadForm.querySelector('button');
    const transcriptDiv = document.getElementById('transcript');
    const clipForm = document.getElementById('clip-form');
    const clipResultDiv = document.getElementById('clip-result');
    let videoFile = '';

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

    const handleClipRequest = async (url, button, form) => {
        button.disabled = true;
        button.textContent = 'Creating...';
        clipResultDiv.innerHTML = `<p>Creating clip... This can take a moment.</p>`;

        const formData = new FormData(form);
        formData.append('videoFile', videoFile);
        if (form.id === 'autoclip-form') {
            formData.append('start', document.getElementById('start').value);
            formData.append('end', document.getElementById('end').value);
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

    clipForm.addEventListener('submit', (e) => {
        e.preventDefault();
        handleClipRequest('/clip', clipButton, clipForm);
    });

    const autoClipForm = document.getElementById('autoclip-form');
    autoClipButton.addEventListener('click', () => {
        handleClipRequest('/autoclip', autoClipButton, autoClipForm);
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
});