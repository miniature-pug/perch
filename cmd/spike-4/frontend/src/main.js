// Prevent the browser drop handler from firing — OnFileDrop needs this.
document.addEventListener('dragover', e => e.preventDefault())
document.addEventListener('drop', e => e.preventDefault())

document.body.innerHTML = `
  <div id="drop-zone" style="width:100%;height:100vh;display:flex;align-items:center;
    justify-content:center;font-family:sans-serif;font-size:18px;
    border:3px dashed #888;box-sizing:border-box;">
    Drop a file here from your file manager
  </div>
`
