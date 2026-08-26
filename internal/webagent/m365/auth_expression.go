package m365

type directAuthObservation struct {
	TokenProviderFound bool           `json:"token_provider_found"`
	TokenProviderError bool           `json:"token_provider_error"`
	AuthToken          string         `json:"auth_token"`
	ComposerFound      bool           `json:"composer_found"`
	ClientMetadata     ClientMetadata `json:"client_metadata"`
}

// m365DirectAuthExpression reads the live app-owned AugLoop token provider.
// Personal accounts can expose the provider while suppressing the visible
// microphone button, so this probe deliberately has no UI-control precondition.
const m365DirectAuthExpression = `(() => {
  const root = document.querySelector('#m365-chat-input-shared-container');
  const visible = element => {
    if (!element || element.disabled) return false;
    const style = window.getComputedStyle(element);
    return style.display !== 'none' && style.visibility !== 'hidden' && element.getClientRects().length > 0;
  };
  const composerFound = [...document.querySelectorAll('[contenteditable="true"], textarea')].some(visible);
  const objectKeys = value => {
    try { return Object.keys(value); } catch (_) { return []; }
  };
  const seenObjects = new WeakSet();
  const seenFibers = new Set();
  let tokenProvider;
  const scanObject = (value, depth) => {
    if (!value || typeof value !== 'object' || seenObjects.has(value) || tokenProvider || depth > 6) return;
    seenObjects.add(value);
    try {
      if (typeof value.tokenProviders?.augloop === 'function') {
        tokenProvider = value.tokenProviders.augloop;
        return;
      }
    } catch (_) {}
    for (const key of objectKeys(value)) {
      try {
        const child = value[key];
        if (child && typeof child === 'object') scanObject(child, depth + 1);
      } catch (_) {}
      if (tokenProvider) return;
    }
  };
  const scanFiber = fiber => {
    if (!fiber || typeof fiber !== 'object' || seenFibers.has(fiber) || tokenProvider || seenFibers.size > 5000) return;
    seenFibers.add(fiber);
    scanObject(fiber.memoizedProps, 0);
    let hook = fiber.memoizedState;
    for (let index = 0; hook && !tokenProvider && index < 120; index += 1, hook = hook.next) {
      scanObject(hook.memoizedState, 0);
    }
    scanFiber(fiber.child);
    scanFiber(fiber.sibling);
  };
  const fiberKey = root && Object.keys(root).find(key => key.startsWith('__reactFiber'));
  let fiber = root && root[fiberKey];
  while (fiber?.return) fiber = fiber.return;
  scanFiber(fiber);
  if (typeof tokenProvider !== 'function') {
    return {token_provider_found: false, composer_found: composerFound};
  }
  return (async () => {
    let authToken = '';
    try {
      authToken = await tokenProvider();
    } catch (_) {
      return {
        token_provider_found: true,
        token_provider_error: true,
        composer_found: composerFound,
      };
    }
    let timezone = 'UTC';
    try { timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || timezone; } catch (_) {}
    return {
      token_provider_found: true,
      auth_token: typeof authToken === 'string' ? authToken : '',
      composer_found: composerFound,
      client_metadata: {
        app_name: 'BizChat',
        app_platform: 'Web',
        app_version: 'Client',
        release_audience_group: 'Production',
        release_channel: '',
        release_fork: '',
        flights: '_acceptsClaimsChallengeMessages;_acceptsSeedingStatusChangeMessages',
        user_system_timezone: timezone,
        runtime_version: '2.37.2567',
      },
    };
  })();
})()`
