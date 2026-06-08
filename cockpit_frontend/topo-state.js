(function (root, factory) {
  if (typeof module === "object" && module.exports) {
    module.exports = factory();
  } else {
    root.CockpitTopoState = factory();
  }
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  const REFRESH_INTERVAL_MS = 15000;

  function applyObjectInPlace(target, source) {
    for (const k of Object.keys(target)) {
      if (!Object.prototype.hasOwnProperty.call(source, k)) delete target[k];
    }
    for (const k of Object.keys(source)) target[k] = source[k];
    return target;
  }

  function applyArrayInPlace(target, items) {
    target.length = 0;
    for (let i = 0; i < items.length; i++) target.push(items[i]);
    return target;
  }

  return {
    applyObjectInPlace,
    applyArrayInPlace,
    REFRESH_INTERVAL_MS,
  };
});
