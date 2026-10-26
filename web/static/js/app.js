document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-dismiss-lang]");
  if (!button) {
    return;
  }
  document.cookie = "tixo_lang_hint_dismissed=1; path=/; max-age=31536000; SameSite=Lax";
  document.getElementById("lang-suggest")?.remove();
});

document.querySelector("[data-focus-on-load]")?.focus();
