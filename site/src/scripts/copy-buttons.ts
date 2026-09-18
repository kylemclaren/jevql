// Adds a copy button to every code block (<pre>) on the page.
const targets = document.querySelectorAll<HTMLElement>("pre")
for (const pre of targets) {
  if (pre.dataset.copyReady || pre.closest(".no-copy")) continue
  pre.dataset.copyReady = "1"
  const wrap = document.createElement("div")
  wrap.className = "copy-wrap"
  pre.parentNode?.insertBefore(wrap, pre)
  wrap.appendChild(pre)
  const btn = document.createElement("button")
  btn.type = "button"
  btn.className = "copy-btn"
  btn.setAttribute("aria-label", "Copy code")
  btn.textContent = "copy"
  btn.addEventListener("click", async () => {
    const text = (pre.querySelector("code") ?? pre).textContent ?? ""
    try {
      await navigator.clipboard.writeText(text.replace(/\n$/, ""))
      btn.textContent = "copied"
      btn.classList.add("done")
    } catch {
      btn.textContent = "failed"
    }
    setTimeout(() => {
      btn.textContent = "copy"
      btn.classList.remove("done")
    }, 1400)
  })
  wrap.appendChild(btn)
}
