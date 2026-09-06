/* Draft preview dispatch only; totals remain calculated by the server. */
(() => { let timer; document.addEventListener("input", event => { if (!event.target.closest("#invoice-editor")) return; clearTimeout(timer); timer = setTimeout(() => document.body.dispatchEvent(new CustomEvent("invoice-preview")), 250); }); })();
