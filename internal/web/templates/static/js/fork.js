// fork.js：本 fork（bootids/go-music-dl）新增的前端功能，独立于上游 app.js，减少合并上游时的冲突。
// 在 index.html 中紧跟 app.js 之后加载：这里的顶层代码会用到 app.js 已定义的 webSettings，
// 其余函数只在 DOMContentLoaded 之后或用户操作时被 app.js 的钩子调用。

// ---------------------------------------------------------------------------
// 设置项：文件整理、Apple Music wrapper
// ---------------------------------------------------------------------------
const FORK_WEB_SETTINGS_DEFAULTS = {
  organizeByArtistAlbum: false,
  cleanAlbumEpisode: false,
  artistAliases: "",
  appleWrapperUrl: "",
  appleAudioQuality: "alac",
};
Object.assign(webSettings, FORK_WEB_SETTINGS_DEFAULTS);

// 由 app.js normalizeWebSettings 调用：补默认值并读取 raw 里的 fork 字段。
function normalizeForkWebSettings(next, raw) {
  Object.assign(next, FORK_WEB_SETTINGS_DEFAULTS);
  if (!raw || typeof raw !== "object") {
    return;
  }
  if (typeof raw.organizeByArtistAlbum === "boolean") {
    next.organizeByArtistAlbum = raw.organizeByArtistAlbum;
  }
  if (typeof raw.cleanAlbumEpisode === "boolean") {
    next.cleanAlbumEpisode = raw.cleanAlbumEpisode;
  }
  if (typeof raw.artistAliases === "string") {
    next.artistAliases = raw.artistAliases.trim();
  }
  if (typeof raw.appleWrapperUrl === "string") {
    next.appleWrapperUrl = raw.appleWrapperUrl.trim();
  }
  if (typeof raw.appleAudioQuality === "string") {
    const quality = raw.appleAudioQuality.trim().toLowerCase();
    next.appleAudioQuality = quality === "aac" ? "aac" : "alac";
  }
  if (typeof raw.appleWrapperConfigured === "boolean") {
    next.appleWrapperConfigured = raw.appleWrapperConfigured;
  }
  if (typeof raw.appleWrapperEnvOverride === "boolean") {
    next.appleWrapperEnvOverride = raw.appleWrapperEnvOverride;
  }
}

// 由 app.js applyWebSettings 调用：把 fork 设置写回设置页表单，并刷新保存预览。
function applyForkWebSettings() {
  const appleWrapperUrlInput = document.getElementById(
    "setting-apple-wrapper-url",
  );
  const appleWrapperState = document.getElementById(
    "setting-apple-wrapper-state",
  );
  if (appleWrapperUrlInput) {
    appleWrapperUrlInput.value = webSettings.appleWrapperUrl;
    appleWrapperUrlInput.disabled = !!webSettings.appleWrapperEnvOverride;
  }
  if (appleWrapperState) {
    appleWrapperState.textContent = webSettings.appleWrapperEnvOverride
      ? "已由环境变量 MUSIC_DL_APPLE_WRAPPER_URL 指定"
      : "";
  }
  const appleAudioQualitySelect = document.getElementById(
    "setting-apple-audio-quality",
  );
  if (appleAudioQualitySelect) {
    appleAudioQualitySelect.value = webSettings.appleAudioQuality;
  }
  const organizeToggle = document.getElementById(
    "setting-organize-by-artist-album",
  );
  if (organizeToggle) {
    organizeToggle.checked = webSettings.organizeByArtistAlbum;
  }
  const cleanAlbumToggle = document.getElementById(
    "setting-clean-album-episode",
  );
  if (cleanAlbumToggle) {
    cleanAlbumToggle.checked = webSettings.cleanAlbumEpisode;
  }
  const artistAliasesInput = document.getElementById("setting-artist-aliases");
  if (artistAliasesInput) {
    artistAliasesInput.value = webSettings.artistAliases;
  }
  bindFileOrganizePreviewEvents();
  refreshFilenamePreview();
}

// 由 app.js saveCookies 调用：从设置页表单读出要保存的 fork 字段。
function collectForkWebSettings() {
  return {
    organizeByArtistAlbum: !!document.getElementById(
      "setting-organize-by-artist-album",
    )?.checked,
    cleanAlbumEpisode: !!document.getElementById(
      "setting-clean-album-episode",
    )?.checked,
    artistAliases:
      document.getElementById("setting-artist-aliases")?.value || "",
    appleWrapperUrl:
      document.getElementById("setting-apple-wrapper-url")?.value || "",
    appleAudioQuality:
      document.getElementById("setting-apple-audio-quality")?.value ||
      "alac",
  };
}

// “文件整理”预览：按表单里尚未保存的模板和开关，请求服务端生成示例保存路径。
const FILE_ORGANIZE_PREVIEW_INPUTS = [
  "setting-download-filename-template",
  "setting-organize-by-artist-album",
  "setting-clean-album-episode",
  "setting-normalize-chinese-variants",
];
let filenamePreviewTimer = null;
let filenamePreviewSeq = 0;

function bindFileOrganizePreviewEvents() {
  FILE_ORGANIZE_PREVIEW_INPUTS.forEach((id) => {
    const el = document.getElementById(id);
    if (!el || el.dataset.previewBound === "1") return;
    el.dataset.previewBound = "1";
    el.addEventListener(el.type === "checkbox" ? "change" : "input", () => {
      clearTimeout(filenamePreviewTimer);
      filenamePreviewTimer = setTimeout(refreshFilenamePreview, 250);
    });
  });
}

function renderFilenamePreviewMessage(list, text) {
  const item = document.createElement("li");
  item.className = "file-organize-preview-empty";
  item.textContent = text;
  list.replaceChildren(item);
}

async function refreshFilenamePreview() {
  const list = document.getElementById("setting-filename-preview");
  if (!list) return;
  const checked = (id) => (document.getElementById(id)?.checked ? "1" : "0");
  const params = new URLSearchParams({
    template:
      document.getElementById("setting-download-filename-template")?.value ||
      "",
    organizeByArtistAlbum: checked("setting-organize-by-artist-album"),
    cleanAlbumEpisode: checked("setting-clean-album-episode"),
    normalizeChineseVariants: checked("setting-normalize-chinese-variants"),
  });
  const seq = ++filenamePreviewSeq;
  try {
    const response = await fetch(
      `${API_ROOT}/download/filename_preview?${params}`,
      { headers: { "X-Requested-With": "XMLHttpRequest" } },
    );
    if (seq !== filenamePreviewSeq) return;
    if (!response.ok) {
      renderFilenamePreviewMessage(
        list,
        response.status === 401 ? "登录后可预览" : `预览失败（HTTP ${response.status}）`,
      );
      return;
    }
    const payload = await response.json();
    if (seq !== filenamePreviewSeq) return;
    const examples = Array.isArray(payload.examples) ? payload.examples : [];
    if (!examples.length) {
      renderFilenamePreviewMessage(list, "暂无预览");
      return;
    }
    list.replaceChildren(
      ...examples.map((example) => {
        const item = document.createElement("li");
        const pathEl = document.createElement("span");
        pathEl.className = "file-organize-preview-path";
        // 每段目录/文件名整体换行，优先在 “/” 处断开，不把名字拆成两行。
        const segments = String(example.path || "").split("/");
        segments.forEach((segment, index) => {
          const isDir = index < segments.length - 1;
          const part = document.createElement("span");
          part.className = isDir
            ? "preview-segment preview-dir"
            : "preview-segment";
          part.textContent = isDir ? `${segment}/` : segment;
          pathEl.append(part);
        });
        item.append(pathEl);
        if (example.albumTag && example.albumTag !== example.album) {
          const tag = document.createElement("span");
          tag.className = "file-organize-preview-tag";
          ["专辑标签：", example.album, " → ", example.albumTag].forEach(
            (text) => {
              const part = document.createElement("span");
              part.className = "preview-segment";
              part.textContent = text;
              tag.append(part);
            },
          );
          item.append(tag);
        }
        return item;
      }),
    );
  } catch (error) {
    if (seq !== filenamePreviewSeq) return;
    renderFilenamePreviewMessage(list, "预览失败");
  }
}

async function testAppleWrapper() {
  const state = document.getElementById("setting-apple-wrapper-state");
  if (!state) return;
  state.textContent = "检测中…";
  // 检测输入框里的地址（可能还没保存）；环境变量锁定时输入框禁用，由服务端用环境变量。
  const input = document.getElementById("setting-apple-wrapper-url");
  const query =
    input && !input.disabled
      ? `?url=${encodeURIComponent(input.value.trim())}`
      : "";
  try {
    const response = await fetch(`${API_ROOT}/apple/wrapper/status${query}`, {
      headers: { "X-Requested-With": "XMLHttpRequest" },
    });
    const payload = await response.json().catch(() => ({}));
    const ffmpeg = payload.ffmpeg ? "ffmpeg ✓" : "ffmpeg ✗";
    if (response.ok && payload.ok) {
      const regions = Array.isArray(payload.regions)
        ? payload.regions.join(", ")
        : "";
      state.textContent = `已连接 · 区域 ${regions} · ${ffmpeg}`;
    } else {
      state.textContent = `${payload.error || `HTTP ${response.status}`} · ${ffmpeg}`;
    }
  } catch (error) {
    state.textContent = String(error?.message || error);
  }
}

// 本地音乐卡片“更多”菜单里的“找逐字歌词”按钮（由 renderLocalMusicPageCard 插入）。
function forkLyricVerbatimButtonHTML() {
  return `<button type="button" class="btn-circle btn-dl btn-lyric-verbatim" title="找逐字歌词" onclick="openLyricUpgradeFromButton(this)">
                            <i class="fa-solid fa-microphone-lines"></i>
                        </button>`;
}

// ---------------------------------------------------------------------------
// 歌词检查：识别本地歌曲歌词类型，逐行歌词可从 QQ 音乐换成逐字并写回文件。
// ---------------------------------------------------------------------------
const LYRIC_KIND_LABELS = {
  verbatim: "逐字",
  line: "逐行",
  plain: "无时间",
  none: "无歌词",
};
const LYRIC_CHECK_FILTERS = ["line", "none", "plain", "verbatim", "all"];
const lyricCheckState = { tracks: [], counts: {}, filter: "line" };

function lyricEl(tag, className, text) {
  const el = document.createElement(tag);
  if (className) el.className = className;
  if (text !== undefined && text !== null) el.textContent = String(text);
  return el;
}

function lyricKindBadge(kind) {
  return lyricEl("span", `lyric-kind-badge is-${kind || "none"}`, LYRIC_KIND_LABELS[kind] || "未知");
}

function ensureLyricCheckModal() {
  let overlay = document.getElementById("lyric-check-modal-overlay");
  if (overlay) return overlay;
  overlay = lyricEl("div", "modal-overlay utility-modal-overlay is-open");
  overlay.id = "lyric-check-modal-overlay";
  overlay.addEventListener("click", (e) => {
    if (e.target === overlay) closeLyricCheckModal();
  });
  const modal = lyricEl("div", "modal utility-modal lyric-check-modal");
  modal.setAttribute("role", "dialog");
  modal.setAttribute("aria-modal", "true");
  modal.setAttribute("aria-labelledby", "lyric-check-title");
  const header = lyricEl("div", "modal-header");
  const titleWrap = lyricEl("div");
  const title = lyricEl("h3");
  title.id = "lyric-check-title";
  title.innerHTML = '<i class="fa-solid fa-microphone-lines"></i> ';
  title.append(lyricEl("span", "lyric-check-title-text", "歌词检查"));
  const subtitle = lyricEl("p", "utility-modal-subtitle lyric-check-subtitle");
  titleWrap.append(title, subtitle);
  const close = lyricEl("button", "modal-close");
  close.type = "button";
  close.setAttribute("aria-label", "关闭歌词检查");
  close.innerHTML = '<i class="fa-solid fa-xmark"></i>';
  close.addEventListener("click", closeLyricCheckModal);
  header.append(titleWrap, close);
  const body = lyricEl("div", "modal-body lyric-check-body");
  modal.append(header, body);
  overlay.append(modal);
  document.body.append(overlay);
  document.addEventListener("keydown", lyricCheckEscHandler);
  return overlay;
}

function lyricCheckEscHandler(e) {
  if (e.key === "Escape") closeLyricCheckModal();
}

// 弹窗每换一次视图（列表、候选搜索、关闭）序号加一；异步请求回来时序号变了就丢弃结果，
// 避免慢的旧请求覆盖当前视图（例如快速换关键词后显示上一次搜索的候选）。
let lyricCheckViewSeq = 0;
let lyricPreviewSeq = 0;

function closeLyricCheckModal() {
  lyricCheckViewSeq += 1;
  document.getElementById("lyric-check-modal-overlay")?.remove();
  document.removeEventListener("keydown", lyricCheckEscHandler);
}

function setLyricCheckHeader(titleText, subtitleText) {
  lyricCheckViewSeq += 1;
  const overlay = ensureLyricCheckModal();
  overlay.querySelector(".lyric-check-title-text").textContent = titleText;
  overlay.querySelector(".lyric-check-subtitle").textContent = subtitleText;
  return overlay.querySelector(".lyric-check-body");
}

function lyricCheckStateMessage(body, icon, text, isError = false) {
  const box = lyricEl("div", `lyric-check-state${isError ? " is-error" : ""}`);
  const i = lyricEl("i", icon);
  box.append(i, lyricEl("div", "", text));
  body.replaceChildren(box);
}

// 网络失败（fetch 抛错）也按失败响应返回 status 0，调用方统一走 !ok 分支显示错误。
async function lyricCheckFetchJSON(url, options = {}) {
  let response;
  try {
    response = await fetch(url, {
      ...options,
      headers: {
        "X-Requested-With": "XMLHttpRequest",
        ...(options.body ? { "Content-Type": "application/json" } : {}),
        ...(options.headers || {}),
      },
    });
  } catch (error) {
    return { ok: false, status: 0, payload: { error: `网络请求失败：${error?.message || error}` } };
  }
  const payload = await response.json().catch(() => ({}));
  return { ok: response.ok, status: response.status, payload };
}

async function openLyricCheckModal(refresh = false) {
  const body = setLyricCheckHeader("歌词检查", "识别下载目录里每首歌的歌词类型；逐行歌词可从 QQ 音乐换成逐字");
  const seq = lyricCheckViewSeq;
  lyricCheckStateMessage(body, "fa-solid fa-spinner fa-spin", "正在读取本地歌词...");
  const { ok, payload } = await lyricCheckFetchJSON(
    `${API_ROOT}/local_music/lyric_check${refresh ? "?refresh=1" : ""}`,
  );
  if (seq !== lyricCheckViewSeq) return;
  if (!ok) {
    lyricCheckStateMessage(body, "fa-solid fa-circle-exclamation", payload.error || "读取失败", true);
    return;
  }
  lyricCheckState.tracks = Array.isArray(payload.tracks) ? payload.tracks : [];
  lyricCheckState.counts = payload.counts || {};
  renderLyricCheckList();
}

function renderLyricCheckList() {
  const body = setLyricCheckHeader("歌词检查", "识别下载目录里每首歌的歌词类型；逐行歌词可从 QQ 音乐换成逐字");
  const toolbar = lyricEl("div", "utility-modal-toolbar lyric-check-toolbar");
  const chips = lyricEl("div", "lyric-check-filters");
  chips.setAttribute("role", "tablist");
  LYRIC_CHECK_FILTERS.forEach((filter) => {
    const count =
      filter === "all" ? lyricCheckState.tracks.length : lyricCheckState.counts[filter] || 0;
    if (filter !== "all" && filter !== "line" && count === 0) return;
    const chip = lyricEl("button", `lyric-check-filter${lyricCheckState.filter === filter ? " is-active" : ""}`);
    chip.type = "button";
    chip.setAttribute("role", "tab");
    chip.setAttribute("aria-selected", lyricCheckState.filter === filter ? "true" : "false");
    chip.append(lyricEl("span", "", filter === "all" ? "全部" : LYRIC_KIND_LABELS[filter]), lyricEl("span", "lyric-check-filter-count", count));
    chip.addEventListener("click", () => {
      lyricCheckState.filter = filter;
      renderLyricCheckList();
    });
    chips.append(chip);
  });
  const rescan = lyricEl("button", "btn-pill lyric-check-rescan");
  rescan.type = "button";
  rescan.innerHTML = '<i class="fa-solid fa-rotate"></i> 重新扫描';
  rescan.addEventListener("click", () => openLyricCheckModal(true));
  toolbar.append(chips, rescan);

  const list = lyricEl("div", "utility-modal-list lyric-check-list");
  const filtered = lyricCheckState.tracks.filter(
    (t) => lyricCheckState.filter === "all" || t.kind === lyricCheckState.filter,
  );
  if (!filtered.length) {
    const empty = lyricEl("div", "lyric-check-state is-clean");
    empty.append(
      lyricEl("i", "fa-solid fa-circle-check"),
      lyricEl("div", "", lyricCheckState.filter === "line" ? "没有逐行歌词的歌曲" : "这一类没有歌曲"),
    );
    list.append(empty);
  }
  filtered.forEach((track) => {
    const row = lyricEl("div", "lyric-check-row");
    const main = lyricEl("div", "lyric-check-row-main");
    main.append(
      lyricEl("div", "lyric-check-row-name", track.name),
      lyricEl("div", "lyric-check-row-meta", [track.artist, track.album].filter(Boolean).join(" · ")),
    );
    const side = lyricEl("div", "lyric-check-row-side");
    side.append(lyricEl("span", "lyric-check-row-duration", formatDuration(track.duration)), lyricKindBadge(track.kind));
    const action = lyricEl("button", "btn-pill lyric-check-row-action");
    action.type = "button";
    if (!track.writable) {
      action.disabled = true;
      action.title = `${(track.ext || "").toUpperCase()} 文件不支持写入内嵌歌词`;
      action.textContent = "不支持";
    } else {
      action.innerHTML = '<i class="fa-solid fa-magnifying-glass"></i> ';
      action.append(document.createTextNode(track.kind === "verbatim" ? "重新查找" : "找逐字歌词"));
      action.addEventListener("click", () => openLyricCandidates(track.id, true));
    }
    side.append(action);
    row.append(main, side);
    list.append(row);
  });
  body.replaceChildren(toolbar, list);
}

function openLyricUpgradeFromButton(button) {
  const card = button?.closest?.(".song-card");
  const id = card?.dataset?.id;
  closeSongActionMenus();
  if (!id) return;
  ensureLyricCheckModal();
  openLyricCandidates(id, false);
}

function lyricDiffLabel(candidate) {
  if (candidate.diff === null || candidate.diff === undefined) {
    return { text: "时长未知", cls: "is-unknown" };
  }
  if (candidate.match) return { text: "时长一致", cls: "is-match" };
  const sign = candidate.diff > 0 ? "+" : "";
  return { text: `相差 ${sign}${candidate.diff} 秒`, cls: "is-mismatch" };
}

async function openLyricCandidates(id, fromList, keyword = "") {
  const body = setLyricCheckHeader("找逐字歌词", "从 QQ 音乐选择同一首歌；时长一致可直接嵌入");
  const seq = lyricCheckViewSeq;
  lyricCheckStateMessage(body, "fa-solid fa-spinner fa-spin", "正在搜索 QQ 音乐...");
  const query = new URLSearchParams({ id });
  if (keyword) query.set("q", keyword);
  const { ok, payload } = await lyricCheckFetchJSON(`${API_ROOT}/local_music/lyric_candidates?${query}`);
  if (seq !== lyricCheckViewSeq) return;
  const local = payload.local || {};
  const view = lyricEl("div", "lyric-candidates");

  if (fromList) {
    const back = lyricEl("button", "lyric-check-back");
    back.type = "button";
    back.innerHTML = '<i class="fa-solid fa-arrow-left"></i> 返回列表';
    back.addEventListener("click", renderLyricCheckList);
    view.append(back);
  }

  const summary = lyricEl("div", "lyric-local-summary");
  const summaryMain = lyricEl("div", "lyric-local-summary-main");
  summaryMain.append(
    lyricEl("div", "lyric-local-summary-name", local.name || "本地歌曲"),
    lyricEl("div", "lyric-local-summary-meta", [local.artist, local.album].filter(Boolean).join(" · ")),
  );
  const summarySide = lyricEl("div", "lyric-local-summary-side");
  summarySide.append(lyricEl("span", "lyric-local-summary-duration", `本地 ${formatDuration(local.duration)}`), lyricKindBadge(local.kind));
  summary.append(summaryMain, summarySide);

  const form = lyricEl("form", "lyric-search-form");
  const input = lyricEl("input", "lyric-search-input");
  input.type = "search";
  input.value = payload.keyword || keyword || "";
  input.setAttribute("aria-label", "QQ 音乐搜索关键词");
  const submit = lyricEl("button", "btn-pill btn-pill-primary");
  submit.type = "submit";
  submit.innerHTML = '<i class="fa-solid fa-magnifying-glass"></i> 搜索';
  form.append(input, submit);
  form.addEventListener("submit", (e) => {
    e.preventDefault();
    openLyricCandidates(id, fromList, input.value.trim());
  });
  view.append(summary, form);

  const list = lyricEl("div", "utility-modal-list lyric-candidate-list");
  const preview = lyricEl("div", "lyric-preview");
  preview.hidden = true;
  if (!ok) {
    lyricCheckStateMessage(list, "fa-solid fa-circle-exclamation", payload.error || "搜索失败", true);
  } else if (!payload.candidates?.length) {
    lyricCheckStateMessage(list, "fa-regular fa-face-meh", "没有搜到候选，换个关键词试试");
  } else {
    payload.candidates.forEach((candidate) => {
      list.append(renderLyricCandidateRow(id, local, candidate, payload.tolerance, preview, fromList));
    });
  }
  view.append(list, preview);
  body.replaceChildren(view);
  if (!ok || !payload.candidates?.length) input.focus();
}

function renderLyricCandidateRow(id, local, candidate, tolerance, preview, fromList) {
  const row = lyricEl("div", "lyric-candidate-row");
  const main = lyricEl("button", "lyric-candidate-main");
  main.type = "button";
  main.title = "预览歌词";
  main.append(
    lyricEl("span", "lyric-candidate-name", candidate.name),
    lyricEl("span", "lyric-candidate-meta", [candidate.artist, candidate.album].filter(Boolean).join(" · ")),
  );
  main.addEventListener("click", () => showLyricPreview(id, local, candidate, preview, fromList, row));
  const diff = lyricDiffLabel(candidate);
  const side = lyricEl("div", "lyric-candidate-side");
  side.append(
    lyricEl("span", "lyric-candidate-duration", formatDuration(candidate.duration)),
    lyricEl("span", `lyric-diff-badge ${diff.cls}`, diff.text),
  );
  const action = lyricEl("button", `btn-pill ${candidate.match ? "btn-pill-primary" : ""} lyric-candidate-action`);
  action.type = "button";
  if (local.writable === false) {
    action.disabled = true;
    action.textContent = "不支持";
  } else if (candidate.match) {
    action.textContent = "嵌入";
    action.addEventListener("click", () => applyLyricCandidate(id, candidate, false, action, fromList));
  } else {
    action.textContent = "预览";
    action.addEventListener("click", () => showLyricPreview(id, local, candidate, preview, fromList, row));
  }
  side.append(action);
  row.append(main, side);
  return row;
}

async function showLyricPreview(id, local, candidate, preview, fromList, row) {
  row.parentElement?.querySelectorAll(".lyric-candidate-row.is-selected").forEach((el) => el.classList.remove("is-selected"));
  row.classList.add("is-selected");
  preview.hidden = false;
  const seq = ++lyricPreviewSeq;
  const viewSeq = lyricCheckViewSeq;
  lyricCheckStateMessage(preview, "fa-solid fa-spinner fa-spin", "正在获取歌词...");
  const { ok, payload } = await lyricCheckFetchJSON(`${API_ROOT}/local_music/lyric_preview`, {
    method: "POST",
    body: JSON.stringify(candidate),
  });
  // 只显示最后一次点开的候选，且视图没换过。
  if (seq !== lyricPreviewSeq || viewSeq !== lyricCheckViewSeq) return;
  if (!ok) {
    lyricCheckStateMessage(preview, "fa-solid fa-circle-exclamation", payload.error || "获取歌词失败", true);
    return;
  }
  const head = lyricEl("div", "lyric-preview-head");
  head.append(lyricEl("span", "lyric-preview-title", `${[candidate.name, candidate.artist].filter(Boolean).join(" · ")} 的歌词`), lyricKindBadge(payload.kind));
  const lines = lyricEl("div", "lyric-preview-lines");
  const textLines = String(payload.lyric || "")
    .split("\n")
    .map((line) => ({
      start: (line.match(/^\[(\d+:\d+(?:\.\d+)?)\]/) || [])[1] || "",
      text: line.replace(/\[[^\]]*\]/g, "").trim(),
    }))
    .filter((line) => line.start && line.text)
    // 预览只看正文：跳过开头的“歌名 - 歌手”和“词：/曲：/编曲：”等署名行（写入的歌词不受影响）。
    .filter((line, index) => !(index === 0 && / - /.test(line.text)) && !/^[^：:]{1,40}[：:]/.test(line.text));
  let shown = 0;
  let lastStart = "";
  for (const line of textLines) {
    const isTranslation = line.start === lastStart;
    if (!isTranslation && shown >= 8) break;
    lines.append(lyricEl("div", `lyric-preview-line${isTranslation ? " is-translation" : ""}`, line.text));
    if (!isTranslation) shown += 1;
    lastStart = line.start;
  }
  if (textLines.length > shown) lines.append(lyricEl("div", "lyric-preview-more", "…"));
  const footer = lyricEl("div", "lyric-preview-footer");
  const diff = lyricDiffLabel(candidate);
  if (!candidate.match) {
    footer.append(
      lyricEl(
        "span",
        "lyric-preview-warning",
        diff.cls === "is-unknown"
          ? "读不到本地时长，无法确认是同一版本"
          : `与本地时长${diff.text}，可能不是同一版本`,
      ),
    );
  }
  const apply = lyricEl("button", `btn-pill ${candidate.match ? "btn-pill-primary" : "lyric-force-button"}`);
  apply.type = "button";
  apply.textContent = candidate.match ? "嵌入歌词" : "仍然嵌入";
  if (local.writable === false) {
    apply.disabled = true;
    apply.textContent = "该格式不支持写入";
  }
  apply.addEventListener("click", () => applyLyricCandidate(id, candidate, !candidate.match, apply, fromList));
  footer.append(apply);
  preview.replaceChildren(head, lines, footer);
  // 预览在候选列表下方，滚到可见，确保“嵌入”按钮不用手动找。
  preview.scrollIntoView({ block: "nearest", behavior: "smooth" });
}

async function applyLyricCandidate(id, candidate, force, button, fromList) {
  const label = button.textContent;
  const viewSeq = lyricCheckViewSeq;
  button.disabled = true;
  button.textContent = "正在写入...";
  let result;
  try {
    result = await lyricCheckFetchJSON(`${API_ROOT}/local_music/lyric_apply`, {
      method: "POST",
      body: JSON.stringify({ id, candidate, force }),
    });
  } finally {
    button.disabled = false;
    button.textContent = label;
  }
  const { ok, status, payload } = result;
  if (!ok) {
    if (status === 409) {
      showToast("时长不一致", "请先预览确认是同一版本，再选择“仍然嵌入”。", "warning", 6000);
    } else if (status === 401) {
      showToast("需要登录", "写入歌词需要管理员登录。", "error", 6000);
    } else {
      showToast("嵌入失败", payload.error || `HTTP ${status}`, "error", 8000);
    }
    return;
  }
  showToast(
    "已嵌入逐字歌词",
    payload.sidecar ? "同名 .lrc 已一并替换，最早的原始歌词保留在 .lrc.bak。" : "歌词已写入歌曲文件。",
    "success",
    5000,
  );
  const track = lyricCheckState.tracks.find((t) => t.id === id);
  if (track && payload.kind) {
    if (lyricCheckState.counts[track.kind]) lyricCheckState.counts[track.kind] -= 1;
    track.kind = payload.kind;
    lyricCheckState.counts[track.kind] = (lyricCheckState.counts[track.kind] || 0) + 1;
  }
  // 写入期间用户已切到别的视图（或关了弹窗）时，不把当前视图拉回列表。
  if (viewSeq !== lyricCheckViewSeq) return;
  if (fromList) {
    renderLyricCheckList();
  } else {
    closeLyricCheckModal();
  }
}
