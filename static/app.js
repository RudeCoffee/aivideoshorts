document.addEventListener('DOMContentLoaded', () => {
    const uploadForm = document.getElementById('upload-form');
    const transcriptDiv = document.getElementById('transcript');
    const clipForm = document.getElementById('clip-form');
    const clipResultDiv = document.getElementById('clip-result');
    let videoFile = '';

    uploadForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        transcriptDiv.innerHTML = '<p>Uploading and transcribing...</p>';
        clipResultDiv.innerHTML = '';

        const formData = new FormData(uploadForm);
        const response = await fetch('/upload', {
            method: 'POST',
            body: formData,
        });

        if (response.ok) {
            const data = await response.json();
            videoFile = data.videoFile;
            const transcript = data.transcript;
            transcriptDiv.innerHTML = ''; // Clear previous transcript
            transcript.forEach(segment => {
                const p = document.createElement('p');
                p.textContent = segment.text;
                p.dataset.start = segment.start;
                p.dataset.end = segment.end;
                p.addEventListener('click', () => {
                    document.getElementById('start').value = segment.start;
                    document.getElementById('end').value = segment.end;
                });
                transcriptDiv.appendChild(p);
            });
        } else {
            transcriptDiv.innerHTML = `<p>Error uploading and transcribing file.</p>`;
        }
    });

    clipForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        clipResultDiv.innerHTML = '<p>Creating clip...</p>';

        const formData = new FormData(clipForm);
        formData.append('videoFile', videoFile);

        const response = await fetch('/clip', {
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
            clipResultDiv.innerHTML = `<p>Error creating clip.</p>`;
        }
    });
});