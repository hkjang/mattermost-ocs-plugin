(function () {
    var storageKey = "ocs-doc-lang";

    function detectLanguage() {
        var stored = window.localStorage.getItem(storageKey);
        if (stored === "ko" || stored === "en") {
            return stored;
        }

        var languages = window.navigator.languages || [window.navigator.language || "en"];
        for (var i = 0; i < languages.length; i += 1) {
            if (/^ko\b/i.test(languages[i])) {
                return "ko";
            }
        }

        return "en";
    }

    function applyLanguage(language) {
        var lang = language === "ko" ? "ko" : "en";
        document.documentElement.lang = lang;
        document.documentElement.dataset.activeLang = lang;

        var buttons = document.querySelectorAll("[data-lang-switch]");
        buttons.forEach(function (button) {
            button.setAttribute("aria-pressed", button.getAttribute("data-lang-switch") === lang ? "true" : "false");
        });

        var title = document.documentElement.getAttribute("data-title-" + lang);
        if (title) {
            document.title = title;
        }
    }

    function setLanguage(language) {
        window.localStorage.setItem(storageKey, language);
        applyLanguage(language);
    }

    document.addEventListener("DOMContentLoaded", function () {
        var initial = detectLanguage();
        applyLanguage(initial);

        var buttons = document.querySelectorAll("[data-lang-switch]");
        buttons.forEach(function (button) {
            button.addEventListener("click", function () {
                setLanguage(button.getAttribute("data-lang-switch"));
            });
        });
    });
})();
