/* Accessible feedback for full-page and HTMX form responses. */
(() => {
  const focusSummary = () => document.getElementById("validation-summary")?.focus();
  document.addEventListener("DOMContentLoaded", focusSummary);
  document.addEventListener("htmx:afterSwap", focusSummary);
  const showFailure = message => {
    let summary = document.getElementById("request-feedback");
    if (!summary) {
      summary = document.createElement("div");
      summary.id = "request-feedback";
      summary.className = "validation";
      summary.setAttribute("role", "alert");
      summary.setAttribute("aria-live", "assertive");
      summary.tabIndex = -1;
      (document.getElementById("main") || document.body).prepend(summary);
    }
    summary.textContent = message;
    summary.focus();
  };
  document.addEventListener("htmx:responseError", event => {
    showFailure(event.detail.xhr.status === 403
      ? "Request rejected. Reload this page and try again."
      : "The request could not be completed. Please try again.");
  });
  for (const event of ["htmx:sendError", "htmx:timeout"])
    document.addEventListener(event, () => showFailure("Connection unavailable. Your entries are still here; please try again."));
})();
