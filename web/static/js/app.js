document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-dismiss-lang]");
  if (!button) {
    return;
  }
  document.cookie = "tixo_lang_hint_dismissed=1; path=/; max-age=31536000; SameSite=Lax";
  document.getElementById("lang-suggest")?.remove();
});

document.querySelector("[data-focus-on-load]")?.focus();

document.addEventListener("submit", (event) => {
  const message = event.target.getAttribute?.("data-confirm");
  if (message && !confirm(message)) {
    event.preventDefault();
  }
});

function formatFileSize(bytes) {
  if (bytes < 1024) {
    return bytes + " B";
  }
  if (bytes < 1024 * 1024) {
    return Math.round(bytes / 1024) + " KB";
  }
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

async function compressImageFile(file, maxDimension, quality) {
  if (!file.type.startsWith("image/") || typeof createImageBitmap !== "function") {
    return file;
  }
  const bitmap = await createImageBitmap(file).catch(() => null);
  if (!bitmap) {
    return file;
  }
  const scale = Math.min(1, maxDimension / Math.max(bitmap.width, bitmap.height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(bitmap.width * scale);
  canvas.height = Math.round(bitmap.height * scale);
  const ctx = canvas.getContext("2d");
  if (!ctx) {
    return file;
  }
  ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  const blob = await new Promise((resolve) => canvas.toBlob(resolve, "image/webp", quality));
  if (!blob || blob.size >= file.size) {
    return file;
  }
  const name = file.name.replace(/\.[^.]+$/, "") + ".webp";
  return new File([blob], name, { type: "image/webp" });
}

function setupPosterCompression() {
  const input = document.getElementById("event-poster");
  const dropzone = document.getElementById("poster-dropzone");
  const preview = document.getElementById("event-poster-preview");
  const status = document.getElementById("event-poster-compress-status");
  if (!input || !dropzone || !preview) {
    return;
  }

  async function handleFile(file) {
    if (!file) {
      return;
    }
    preview.src = URL.createObjectURL(file);
    preview.classList.remove("d-none");
    const before = file.size;
    if (status) {
      status.textContent = status.getAttribute("data-msg-compressing") || "";
    }
    const compressed = await compressImageFile(file, 1600, 0.82);
    const dt = new DataTransfer();
    dt.items.add(compressed);
    input.files = dt.files;
    if (status) {
      const template = status.getAttribute("data-msg-compressed") || "";
      status.textContent = compressed === file
        ? ""
        : template.replace("{before}", formatFileSize(before)).replace("{after}", formatFileSize(compressed.size));
    }
  }

  input.addEventListener("change", () => handleFile(input.files[0]));

  dropzone.addEventListener("dragover", (event) => {
    event.preventDefault();
    dropzone.classList.add("is-dragover");
  });
  dropzone.addEventListener("dragleave", () => dropzone.classList.remove("is-dragover"));
  dropzone.addEventListener("drop", (event) => {
    event.preventDefault();
    dropzone.classList.remove("is-dragover");
    const file = event.dataTransfer?.files?.[0];
    if (file) {
      handleFile(file);
    }
  });
}

function setupLineupAutocomplete() {
  const select = document.querySelector(".js-lineup-select");
  const form = document.querySelector(".js-lineup-select-form");
  const fallback = document.querySelector(".js-lineup-fallback");
  if (!select || !form || typeof window.jQuery === "undefined" || typeof window.jQuery.fn.select2 !== "function") {
    return;
  }
  form.classList.remove("d-none");
  fallback?.classList.add("d-none");

  window.jQuery(select).select2({
    theme: "bootstrap-5",
    width: "100%",
    minimumInputLength: 3,
    delay: 300,
    placeholder: select.dataset.placeholder || "",
    language: {
      noResults: () => select.dataset.noResults || "",
      searching: () => select.dataset.searching || "",
    },
    ajax: {
      url: form.dataset.searchUrl,
      dataType: "json",
      data: (params) => ({ q: params.term }),
      processResults: (data) => ({ results: data.map((item) => ({ id: item.id, text: item.label })) }),
    },
  });

  const label = document.querySelector(`label[for="${select.id}"]`);
  const combobox = select.nextElementSibling?.querySelector('[role="combobox"]');
  if (label && combobox) {
    const labelId = `${select.id}-label`;
    label.id = labelId;
    const existing = combobox.getAttribute("aria-labelledby") || "";
    combobox.setAttribute("aria-labelledby", `${labelId} ${existing}`.trim());
  }

  window.jQuery(select).on("select2:select", () => form.submit());

  new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      for (const node of mutation.addedNodes) {
        if (node.nodeType === Node.ELEMENT_NODE && node.classList.contains("select2-results__message")) {
          node.setAttribute("role", "option");
          node.setAttribute("aria-disabled", "true");
        }
      }
    }
  }).observe(document.body, { childList: true, subtree: true });
}

setupPosterCompression();
setupLineupAutocomplete();
