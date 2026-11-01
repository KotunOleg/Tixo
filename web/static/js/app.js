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

function setupBuyBar() {
  const bar = document.querySelector(".js-buybar");
  const buy = bar?.querySelector(".js-buybar-buy");
  const tickets = document.getElementById("tickets");
  if (!bar || !buy || !tickets || typeof IntersectionObserver !== "function") {
    return;
  }
  const target = bar.querySelector(".tixo-bar-contact") ? buy : bar;
  new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.isIntersecting && target.contains(document.activeElement)) {
        tickets.focus();
      }
      target.hidden = entry.isIntersecting;
    }
  }).observe(tickets);
}

function setupVenueMap() {
  const el = document.querySelector(".js-venue-map");
  if (!el || typeof L === "undefined") {
    return;
  }
  const lat = Number(el.dataset.lat);
  const lng = Number(el.dataset.lng);
  if (!Number.isFinite(lat) || !Number.isFinite(lng)) {
    return;
  }
  el.hidden = false;
  const map = L.map(el, { scrollWheelZoom: false }).setView([lat, lng], 16);
  map.attributionControl.setPrefix(false);
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: el.dataset.attribution,
  }).addTo(map);
  const tokens = getComputedStyle(document.documentElement);
  L.circleMarker([lat, lng], {
    radius: 10,
    weight: 3,
    color: tokens.getPropertyValue("--tixo-bg").trim(),
    fillColor: tokens.getPropertyValue("--tixo-accent").trim(),
    fillOpacity: 1,
  }).addTo(map);
}

setupBuyBar();
setupVenueMap();

function readJSONScript(id) {
  const source = document.getElementById(id);
  if (!source) {
    return null;
  }
  try {
    return JSON.parse(source.textContent);
  } catch {
    return null;
  }
}

function makeElement(tag, className, text) {
  const el = document.createElement(tag);
  if (className) {
    el.className = className;
  }
  if (text !== undefined) {
    el.textContent = text;
  }
  return el;
}

function mapEventItem(event) {
  const item = document.createElement("li");
  const link = makeElement("a", "", event.title);
  link.href = event.url;
  const meta = makeElement("span", "d-block small text-body-secondary");
  const time = makeElement("time", "", event.when);
  time.dateTime = event.starts_at;
  meta.append(time, " · " + event.facts);
  item.append(link, meta);
  return item;
}

function mapVenueEvents(venue) {
  const list = makeElement("ul", "tixo-venue-events list-unstyled mb-0");
  venue.events.forEach((event) => list.append(mapEventItem(event)));
  return list;
}

function mapVenuePopup(venue) {
  const box = makeElement("div", "tixo-map-popup");
  box.append(makeElement("p", "tixo-map-popup-title mb-1", venue.name), makeElement("p", "small text-body-secondary mb-2", venue.place), mapVenueEvents(venue));
  if (venue.more_label) {
    box.append(makeElement("p", "small text-body-secondary mt-2 mb-0", venue.more_label));
  }
  return box;
}

function mapResultsList(venues) {
  const list = makeElement("ol", "tixo-venue-results list-unstyled mb-0");
  list.setAttribute("aria-labelledby", "map-results-title");
  venues.forEach((venue) => {
    const item = makeElement("li", "tixo-venue-result");
    item.append(makeElement("h3", "h5 mb-1", venue.name), makeElement("p", "small text-body-secondary mb-2", venue.place), mapVenueEvents(venue));
    if (venue.more_label) {
      item.append(makeElement("p", "small text-body-secondary mt-2 mb-0", venue.more_label));
    }
    list.append(item);
  });
  return list;
}

function mapEmptyState(texts, filtered) {
  const box = makeElement("div", "tixo-empty p-4");
  box.append(makeElement("h3", "h5", texts.empty_title), makeElement("p", "text-body-secondary mb-0", texts.empty_text));
  if (filtered) {
    const reset = makeElement("a", "btn btn-quiet mt-3", texts.reset);
    reset.href = texts.reset_url;
    box.append(reset);
  }
  return box;
}

const mapFieldIDs = { from: "map-from", to: "map-to", genre: "map-genre", max_price: "map-max-price" };

function clearMapFieldErrors(form) {
  document.getElementById("map-errors")?.remove();
  form.querySelectorAll(".invalid-feedback").forEach((el) => el.remove());
  Object.values(mapFieldIDs).forEach((id) => {
    const field = document.getElementById(id);
    field.classList.remove("is-invalid");
    field.removeAttribute("aria-invalid");
    const help = document.getElementById(id + "-help");
    if (help) {
      field.setAttribute("aria-describedby", help.id);
    } else {
      field.removeAttribute("aria-describedby");
    }
  });
}

function showMapFieldErrors(fields, texts) {
  Object.entries(fields).forEach(([name, code]) => {
    const field = document.getElementById(mapFieldIDs[name]);
    const message = texts.errors[name + "_" + code];
    if (!field || !message) {
      return;
    }
    const error = makeElement("div", "invalid-feedback d-block", message);
    error.id = field.id + "-error";
    field.classList.add("is-invalid");
    field.setAttribute("aria-invalid", "true");
    field.setAttribute("aria-describedby", [field.getAttribute("aria-describedby"), error.id].filter(Boolean).join(" "));
    (document.getElementById(field.id + "-help") ?? field).after(error);
  });
}

function keepQueryInLanguageLinks(query) {
  document.querySelectorAll("a[hreflang]").forEach((link) => {
    const url = new URL(link.href);
    url.search = query;
    link.href = url.pathname + url.search;
  });
}

function setupVenueFilterMap() {
  const el = document.querySelector(".js-filter-map");
  const form = document.getElementById("map-filters");
  const data = readJSONScript("map-data");
  const texts = readJSONScript("map-texts");
  if (!el || !form || !data || !texts || typeof L === "undefined" || typeof fetch !== "function") {
    return;
  }
  const results = document.querySelector(".js-map-results");
  const title = document.getElementById("map-results-title");
  const status = document.querySelector(".js-map-status");
  const reset = form.querySelector(".js-map-reset");
  const skip = document.querySelector(".js-map-skip");
  const tokens = getComputedStyle(document.documentElement);
  const ukraine = L.latLngBounds([44.4, 22.1], [52.4, 40.2]);

  el.hidden = false;
  if (skip) {
    skip.hidden = false;
  }
  const map = L.map(el, { scrollWheelZoom: false, keyboard: true }).fitBounds(ukraine);
  map.attributionControl.setPrefix(false);
  L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", { maxZoom: 19, attribution: el.dataset.attribution }).addTo(map);
  const markers = L.layerGroup().addTo(map);
  const pin = L.divIcon({ className: "tixo-map-pin", iconSize: [22, 22], iconAnchor: [11, 11], popupAnchor: [0, -12] });

  function drawMarkers(venues) {
    markers.clearLayers();
    if (!venues.length) {
      map.fitBounds(ukraine);
      return;
    }
    const bounds = [];
    venues.forEach((venue) => {
      const marker = L.marker([venue.lat, venue.lng], { icon: pin, keyboard: true, title: venue.name, alt: venue.name });
      marker.bindPopup(() => mapVenuePopup(venue), { maxWidth: 280, minWidth: 200 });
      marker.addTo(markers);
      marker.getElement()?.setAttribute("aria-label", venue.name);
      bounds.push([venue.lat, venue.lng]);
    });
    map.fitBounds(bounds, { padding: [32, 32], maxZoom: 14 });
  }

  function filterQuery() {
    const params = new URLSearchParams();
    new FormData(form).forEach((value, name) => {
      const trimmed = String(value).trim();
      if (trimmed !== "") {
        params.set(name, trimmed);
      }
    });
    return params;
  }

  function render(next, params) {
    const filtered = [...params.keys()].length > 0;
    title.textContent = next.summary;
    results.replaceChildren(next.venues.length ? mapResultsList(next.venues) : mapEmptyState(texts, filtered));
    reset.hidden = !(filtered && next.venues.length);
    drawMarkers(next.venues);
  }

  let controller = null;
  let latest = 0;
  async function refresh() {
    const params = filterQuery();
    const query = params.toString();
    controller?.abort();
    controller = new AbortController();
    const request = ++latest;
    try {
      const response = await fetch(el.dataset.endpoint + (query ? "?" + query : ""), {
        headers: { Accept: "application/json" },
        signal: controller.signal,
      });
      const body = await response.json();
      if (request !== latest) {
        return;
      }
      clearMapFieldErrors(form);
      if (response.status === 400 && body.fields) {
        showMapFieldErrors(body.fields, texts);
        status.textContent = texts.errors_title;
        title.textContent = "";
        results.replaceChildren();
        reset.hidden = false;
        drawMarkers([]);
        return;
      }
      if (!response.ok) {
        throw new Error("map request failed with " + response.status);
      }
      status.textContent = "";
      render(body, params);
      history.replaceState(null, "", location.pathname + (query ? "?" + query : ""));
      keepQueryInLanguageLinks(query);
    } catch (error) {
      if (error.name === "AbortError" || request !== latest) {
        return;
      }
      status.textContent = texts.network_error;
    }
  }

  let timer = 0;
  const schedule = () => {
    clearTimeout(timer);
    timer = setTimeout(refresh, 300);
  };
  form.addEventListener("input", schedule);
  form.addEventListener("change", schedule);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    clearTimeout(timer);
    refresh();
  });

  drawMarkers(data.venues);
}

setupVenueFilterMap();

function truncateLabel(label, max) {
  const chars = Array.from(label);
  return chars.length > max ? chars.slice(0, max - 1).join("") + "…" : label;
}

function setupAnalyticsCharts() {
  const source = document.getElementById("analytics-data");
  const canvases = document.querySelectorAll(".js-analytics-chart");
  if (!source || !canvases.length || typeof Chart === "undefined") {
    return;
  }
  let data;
  try {
    data = JSON.parse(source.textContent);
  } catch {
    return;
  }
  const tokens = getComputedStyle(document.documentElement);
  const token = (name) => tokens.getPropertyValue(name).trim();
  const accent = token("--tixo-accent");
  const grid = token("--tixo-border");
  const muted = token("--tixo-text-muted");
  const surface = token("--tixo-surface-2");
  const text = token("--tixo-text");
  const locale = data.lang === "uk" ? "uk-UA" : "en-GB";
  const integer = new Intl.NumberFormat(locale, { maximumFractionDigits: 0 });
  const money = new Intl.NumberFormat(locale, { style: "currency", currency: "UAH", currencyDisplay: "narrowSymbol", minimumFractionDigits: 0, maximumFractionDigits: 2 });
  const shortDate = new Intl.DateTimeFormat(locale, { day: "numeric", month: "short", timeZone: "UTC" });
  const longDate = new Intl.DateTimeFormat(locale, { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" });
  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  Chart.defaults.font.family = token("--tixo-font-body") || "Inter, sans-serif";
  Chart.defaults.font.size = 13;
  Chart.defaults.color = muted;

  const tooltip = {
    backgroundColor: surface,
    borderColor: token("--tixo-border-strong"),
    borderWidth: 1,
    titleColor: text,
    bodyColor: text,
    footerColor: muted,
    padding: 10,
    displayColors: false,
  };
  const valueAxis = {
    beginAtZero: true,
    grid: { color: grid },
    border: { display: false },
    ticks: { precision: 0, callback: (v) => integer.format(v) },
  };
  const categoryAxis = { grid: { display: false }, border: { color: grid } };
  const base = {
    responsive: true,
    maintainAspectRatio: false,
    animation: reduceMotion ? false : { duration: 250 },
    plugins: { legend: { display: false }, tooltip },
  };

  const builders = {
    by_day: () => {
      const dates = data.days.map((d) => new Date(d.date + "T00:00:00Z"));
      return {
        type: "line",
        data: {
          labels: dates.map((d) => shortDate.format(d)),
          datasets: [{ label: data.ticketsLabel, data: data.days.map((d) => d.tickets), borderColor: accent, backgroundColor: accent, borderWidth: 2, pointRadius: 0, pointHoverRadius: 5, pointHitRadius: 12, tension: 0 }],
        },
        options: {
          ...base,
          interaction: { mode: "index", intersect: false },
          scales: { x: { ...categoryAxis, ticks: { maxRotation: 0, autoSkipPadding: 12 } }, y: valueAxis },
          plugins: {
            ...base.plugins,
            tooltip: {
              ...tooltip,
              callbacks: {
                title: (items) => longDate.format(dates[items[0].dataIndex]),
                label: (item) => `${data.ticketsLabel}: ${integer.format(item.parsed.y)}`,
                footer: (items) => `${data.revenueLabel}: ${money.format(data.days[items[0].dataIndex].revenue / 100)}`,
              },
            },
          },
        },
      };
    },
    by_event: () => ({
      type: "bar",
      data: {
        labels: data.eventsTickets.map((e) => e.label),
        datasets: [{ label: data.ticketsLabel, data: data.eventsTickets.map((e) => e.value), backgroundColor: accent, borderRadius: 4, borderSkipped: "start", maxBarThickness: 24 }],
      },
      options: {
        ...base,
        indexAxis: "y",
        scales: {
          x: valueAxis,
          y: { ...categoryAxis, ticks: { autoSkip: false, callback(value) { return truncateLabel(this.getLabelForValue(value), window.innerWidth < 576 ? 14 : 28); } } },
        },
        plugins: {
          ...base.plugins,
          tooltip: { ...tooltip, callbacks: { label: (item) => `${data.ticketsLabel}: ${integer.format(item.parsed.x)}` } },
        },
      },
    }),
    by_genre: () => ({
      type: "bar",
      data: {
        labels: data.genres.map((g) => g.label),
        datasets: [{ label: data.eventsLabel, data: data.genres.map((g) => g.value), backgroundColor: accent, borderRadius: 4, borderSkipped: "start", maxBarThickness: 48 }],
      },
      options: {
        ...base,
        scales: { x: { ...categoryAxis, ticks: { autoSkip: false, maxRotation: 45 } }, y: valueAxis },
        plugins: {
          ...base.plugins,
          tooltip: { ...tooltip, callbacks: { label: (item) => `${data.eventsLabel}: ${integer.format(item.parsed.y)}` } },
        },
      },
    }),
  };

  canvases.forEach((canvas) => {
    const build = builders[canvas.dataset.chart];
    const wrapper = canvas.parentElement;
    if (!build || !wrapper) {
      return;
    }
    if (canvas.dataset.chart === "by_event") {
      wrapper.style.height = `${Math.max(10, data.eventsTickets.length * 2.25 + 3)}rem`;
    }
    wrapper.hidden = false;
    try {
      new Chart(canvas, build());
    } catch {
      wrapper.hidden = true;
      return;
    }
    const details = wrapper.parentElement.querySelector(".tixo-chart-data");
    if (details) {
      details.open = false;
    }
  });
}

setupAnalyticsCharts();

function pushGA4Purchase() {
  const source = document.getElementById("ga-purchase");
  if (!source) {
    return;
  }
  let payload;
  try {
    payload = JSON.parse(source.textContent);
  } catch {
    return;
  }
  window.dataLayer = window.dataLayer || [];
  window.dataLayer.push({ event: "purchase", ecommerce: payload });
}

pushGA4Purchase();

function setupTelegramLinkStatus() {
  const section = document.querySelector(".js-telegram-link");
  const rendered = section?.dataset.telegramState;
  const url = section?.dataset.statusUrl;
  if (!section || !url || rendered === "linked" || typeof fetch !== "function") {
    return;
  }
  const pollEveryMs = 3000;
  const pollForMs = 15 * 60 * 1000;
  let deadline = 0;
  let timer = 0;
  let busy = false;

  const visible = () => document.visibilityState === "visible";
  const cancel = () => {
    clearTimeout(timer);
    timer = 0;
  };
  const stop = () => {
    cancel();
    deadline = 0;
  };
  const schedule = () => {
    if (deadline && !timer && visible()) {
      timer = setTimeout(check, pollEveryMs);
    }
  };

  async function check() {
    timer = 0;
    if (!deadline || busy || !visible()) {
      return;
    }
    if (Date.now() > deadline) {
      stop();
      return;
    }
    busy = true;
    let state;
    try {
      const response = await fetch(url, { headers: { Accept: "application/json" }, cache: "no-store" });
      if (!response.ok) {
        throw new Error(String(response.status));
      }
      state = (await response.json()).state;
    } catch {
      stop();
      return;
    } finally {
      busy = false;
    }
    if (state !== rendered) {
      stop();
      location.reload();
      return;
    }
    schedule();
  }

  const start = () => {
    deadline = Date.now() + pollForMs;
    cancel();
    schedule();
  };

  document.addEventListener("visibilitychange", () => {
    cancel();
    if (visible()) {
      check();
    }
  });
  section.addEventListener("submit", start);
  if (rendered === "pending") {
    start();
  }
}

setupTelegramLinkStatus();
