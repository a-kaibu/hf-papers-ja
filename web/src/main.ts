import "./style.css";

const PAPERS_JSON_URL = "https://a-kaibu.github.io/hf-papers-ja/data/papers.json";

type Paper = {
  id: string;
  url: string;
  guid: string;
  publishedAt: string;
  authors: string[];
  institution?: string;
  arxivUrl?: string;
  alphaxivUrl?: string;
  pdfUrl?: string;
  source: PaperText;
  japanese: PaperText;
  tags: string[];
};

type PaperText = {
  title: string;
  summary?: string;
  explanation?: string;
};

type PapersOutput = {
  generatedAt: string;
  sourceFeedUrl: string;
  papers: Paper[];
};

type AppState = {
  data: PapersOutput | null;
  error: string | null;
  query: string;
  selectedTag: string | null;
};

const app = document.querySelector<HTMLDivElement>("#app");

if (!app) {
  throw new Error("#app element was not found");
}

const state: AppState = {
  data: null,
  error: null,
  query: "",
  selectedTag: null,
};

const escapeHtml = (value: string): string =>
  value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");

const formatDate = (value: string): string => {
  if (!value) {
    return "公開日不明";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat("ja-JP", {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(date);
};

const formatGeneratedAt = (value: string): string => {
  if (!value) {
    return "更新日時不明";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return new Intl.DateTimeFormat("ja-JP", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
};

const isPaperText = (value: unknown): value is PaperText => {
  if (!value || typeof value !== "object") {
    return false;
  }

  const text = value as Partial<PaperText>;
  return (
    typeof text.title === "string" &&
    (text.summary === undefined || typeof text.summary === "string") &&
    (text.explanation === undefined || typeof text.explanation === "string")
  );
};

const isPaper = (value: unknown): value is Paper => {
  if (!value || typeof value !== "object") {
    return false;
  }

  const paper = value as Partial<Paper>;
  return (
    typeof paper.id === "string" &&
    typeof paper.url === "string" &&
    typeof paper.guid === "string" &&
    typeof paper.publishedAt === "string" &&
    Array.isArray(paper.authors) &&
    isPaperText(paper.source) &&
    isPaperText(paper.japanese) &&
    Array.isArray(paper.tags)
  );
};

const isPapersOutput = (value: unknown): value is PapersOutput => {
  if (!value || typeof value !== "object") {
    return false;
  }

  const output = value as Partial<PapersOutput>;
  return (
    typeof output.generatedAt === "string" &&
    typeof output.sourceFeedUrl === "string" &&
    Array.isArray(output.papers) &&
    output.papers.every(isPaper)
  );
};

const getAllTags = (papers: Paper[]): string[] => {
  const tagCounts = new Map<string, number>();

  for (const paper of papers) {
    for (const tag of paper.tags) {
      tagCounts.set(tag, (tagCounts.get(tag) ?? 0) + 1);
    }
  }

  return [...tagCounts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0], "ja"))
    .map(([tag]) => tag);
};

const normalizeSearchText = (value: string): string => value.normalize("NFKC").toLowerCase();

const matchesQuery = (paper: Paper, query: string): boolean => {
  const terms = normalizeSearchText(query).trim().split(/\s+/).filter(Boolean);

  if (terms.length === 0) {
    return true;
  }

  const haystack = [
    paper.japanese.title,
    paper.japanese.summary ?? "",
    paper.japanese.explanation ?? "",
    paper.source.title,
    paper.source.summary ?? "",
    paper.source.explanation ?? "",
    ...paper.authors,
  ]
    .join(" ")
    .normalize("NFKC")
    .toLowerCase();

  return terms.every((term) => haystack.includes(term));
};

const getVisiblePapers = (): Paper[] => {
  const papers = state.data?.papers ?? [];

  return papers.filter((paper) => {
    const tagMatches = state.selectedTag ? paper.tags.includes(state.selectedTag) : true;
    return tagMatches && matchesQuery(paper, state.query);
  });
};

const renderMetaLink = (href: string | undefined, label: string): string => {
  if (!href) {
    return "";
  }

  return `<a class="paper-card__meta-link" href="${escapeHtml(href)}" target="_blank" rel="noreferrer">${label}</a>`;
};

const renderPaper = (paper: Paper): string => {
  const authors = paper.authors.length > 0 ? paper.authors.join(", ") : "著者情報なし";
  const summary = paper.japanese.summary ?? "";
  const explanation = paper.japanese.explanation ?? "";
  const sourceSummary = paper.source.summary ?? "";
  const sourceExplanation = paper.source.explanation ?? "";
  const tags = paper.tags.map((tag) => `<span class="tag">${escapeHtml(tag)}</span>`).join("");
  const detailsId = `paper-details-${paper.id.replaceAll(/[^a-zA-Z0-9_-]/g, "-")}`;

  return `
    <article class="paper-card" tabindex="0" aria-expanded="false" aria-controls="${escapeHtml(detailsId)}">
      <div class="paper-card__top">
        <div class="paper-card__meta">
          <time datetime="${escapeHtml(paper.publishedAt)}">${escapeHtml(formatDate(paper.publishedAt))}</time>
          ${renderMetaLink(paper.url, "Paper")}
          ${renderMetaLink(paper.arxivUrl, "arXiv")}
          ${renderMetaLink(paper.alphaxivUrl, "alphaXiv")}
          ${paper.institution ? `<span>${escapeHtml(paper.institution)}</span>` : ""}
        </div>
        <span class="paper-card__toggle-cue" aria-hidden="true"></span>
      </div>
      <h2>${escapeHtml(paper.japanese.title)}</h2>
      <p class="paper-card__authors">${escapeHtml(authors)}</p>
      ${summary ? `<p class="paper-card__summary">${escapeHtml(summary)}</p>` : ""}
      ${explanation ? `<p class="paper-card__explanation">${escapeHtml(explanation)}</p>` : ""}
      <div class="paper-card__tags">${tags}</div>
      <section id="${escapeHtml(detailsId)}" class="paper-card__toggle" hidden>
        <dl class="paper-card__details">
          <div>
            <dt>Title</dt>
            <dd>${escapeHtml(paper.source.title)}</dd>
          </div>
          ${
            sourceSummary
              ? `<div>
                  <dt>Summary</dt>
                  <dd>${escapeHtml(sourceSummary)}</dd>
                </div>`
              : ""
          }
          ${
            sourceExplanation
              ? `<div>
                  <dt>Abstract</dt>
                  <dd>${escapeHtml(sourceExplanation)}</dd>
                </div>`
              : ""
          }
          <div>
            <dt>Authors</dt>
            <dd>${escapeHtml(authors)}</dd>
          </div>
        </dl>
      </section>
    </article>
  `;
};

const setPaperCardExpanded = (card: HTMLElement, expanded: boolean): void => {
  const detailsId = card.getAttribute("aria-controls");
  const details = detailsId ? document.getElementById(detailsId) : null;

  card.setAttribute("aria-expanded", String(expanded));
  if (details) {
    details.hidden = !expanded;
  }
};

const togglePaperCard = (card: HTMLElement): void => {
  setPaperCardExpanded(card, card.getAttribute("aria-expanded") !== "true");
};

const renderResults = (): void => {
  if (!state.data) {
    return;
  }

  const visiblePapers = getVisiblePapers();
  const selectedTagLabel = state.selectedTag ? `タグ: ${state.selectedTag}` : "すべて";
  const paperCountLabel = `${visiblePapers.length} / ${state.data.papers.length} 件`;
  const resultBar = document.querySelector<HTMLElement>(".result-bar");
  const paperList = document.querySelector<HTMLElement>(".paper-list");

  if (resultBar) {
    resultBar.innerHTML = `
      <span>${escapeHtml(paperCountLabel)}</span>
      <span>${escapeHtml(selectedTagLabel)}</span>
    `;
  }

  if (paperList) {
    paperList.innerHTML =
      visiblePapers.length > 0
        ? visiblePapers.map(renderPaper).join("")
        : `<div class="empty-state">
            <h2>条件に一致する論文はありません</h2>
            <p>検索語句やタグの選択を変更してください。</p>
          </div>`;
  }
};

const renderLoading = (): void => {
  app.innerHTML = `
    <main class="app-shell">
      <section class="status-panel">
        <p class="eyebrow">HF Papers JA</p>
        <h1>論文データを読み込んでいます</h1>
        <p>GitHub Pages 上の JSON を取得しています。</p>
      </section>
    </main>
  `;
};

const renderError = (message: string): void => {
  app.innerHTML = `
    <main class="app-shell">
      <section class="status-panel status-panel--error">
        <p class="eyebrow">HF Papers JA</p>
        <h1>Pages 上の JSON を読み込めませんでした</h1>
        <p>${escapeHtml(message)}</p>
        <a href="${PAPERS_JSON_URL}" target="_blank" rel="noreferrer">JSON を直接開く</a>
      </section>
    </main>
  `;
};

const renderApp = (): void => {
  if (state.error) {
    renderError(state.error);
    return;
  }

  if (!state.data) {
    renderLoading();
    return;
  }

  const allTags = getAllTags(state.data.papers);

  app.innerHTML = `
    <main class="app-shell">
      <header class="site-header">
        <div>
          <p class="eyebrow">Hugging Face Daily Papers</p>
          <h1>Hugging Face Daily Papers 日本語まとめ</h1>
          <p class="lead">Hugging Face の論文情報を、日本語の要約と解説で確認できます。</p>
        </div>
        <div class="source-panel">
          <span>更新: ${escapeHtml(formatGeneratedAt(state.data.generatedAt))}</span>
          <a href="${PAPERS_JSON_URL.replace("papers.json", "rss.xml")}" target="_blank" rel="noreferrer">RSS</a>
          <a href="${PAPERS_JSON_URL}" target="_blank" rel="noreferrer">JSON</a>
        </div>
      </header>

      <section class="controls" aria-label="検索と絞り込み">
        <label class="search-field">
          <span>検索</span>
          <input id="search-input" type="search" value="${escapeHtml(state.query)}" placeholder="タイトル、abstract、著者で検索" autocomplete="off" />
        </label>
        <div class="tag-filter" aria-label="タグ絞り込み">
          <button class="tag-button ${state.selectedTag === null ? "is-active" : ""}" type="button" data-tag="">すべて</button>
          ${allTags
            .map(
              (tag) =>
                `<button class="tag-button ${state.selectedTag === tag ? "is-active" : ""}" type="button" data-tag="${escapeHtml(tag)}">${escapeHtml(tag)}</button>`,
            )
            .join("")}
        </div>
      </section>

      <section class="result-bar" aria-live="polite"></section>

      <section class="paper-list" aria-label="論文一覧"></section>
    </main>
  `;

  renderResults();
  bindEvents();
};

const bindEvents = (): void => {
  const searchInput = document.querySelector<HTMLInputElement>("#search-input");
  searchInput?.addEventListener("input", (event) => {
    state.query = (event.target as HTMLInputElement).value;
    renderResults();
  });

  for (const button of document.querySelectorAll<HTMLButtonElement>(".tag-button[data-tag]")) {
    button.addEventListener("click", () => {
      const tag = button.dataset.tag ?? "";
      state.selectedTag = tag === "" || state.selectedTag === tag ? null : tag;
      renderApp();
    });
  }

  const paperList = document.querySelector<HTMLElement>(".paper-list");
  paperList?.addEventListener("click", (event) => {
    const target = event.target as HTMLElement;
    if (target.closest("a, button, input, select, textarea")) {
      return;
    }

    const card = target.closest<HTMLElement>(".paper-card");
    if (card) {
      togglePaperCard(card);
    }
  });

  paperList?.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") {
      return;
    }

    const card =
      event.target instanceof HTMLElement ? event.target.closest<HTMLElement>(".paper-card") : null;
    if (card && event.target === card) {
      event.preventDefault();
      togglePaperCard(card);
    }
  });
};

const loadPapers = async (): Promise<void> => {
  renderLoading();

  try {
    const response = await fetch(PAPERS_JSON_URL, { cache: "no-store" });
    if (!response.ok) {
      throw new Error(`HTTP ${response.status} ${response.statusText}`);
    }

    const json: unknown = await response.json();
    if (!isPapersOutput(json)) {
      throw new Error("JSON の形式が想定と異なります。");
    }

    state.data = json;
  } catch (error) {
    state.error = error instanceof Error ? error.message : "不明なエラーが発生しました。";
  }

  renderApp();
};

void loadPapers();
