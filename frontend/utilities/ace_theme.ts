/* eslint-disable */
// @ts-nocheck
ace.define(
  "ace/theme/automdm",
  ["require", "exports", "module", "ace/lib/dom"],
  function (acequire, exports, module) {
    exports.isDark = false;
    exports.cssClass = "ace-automdm";
    exports.cssText = require("./ace_theme.css");

    var dom = acequire("../lib/dom");
    dom.importCssString(exports.cssText, exports.cssClass);
  }
);
