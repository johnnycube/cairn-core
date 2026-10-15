import { paraglideMiddleware } from '#lib/paraglide/server.js';
import { sequence, type Handle } from '@sveltejs/kit/hooks';

// Locale resolution: Paraglide's middleware reads PARAGLIDE_LOCALE
// cookie → Accept-Language header → baseLocale (de). The %lang%
// placeholder in app.html receives the resolved value so the <html>
// element has the right lang attribute for screen readers and search.
const paraglideHandle: Handle = ({ event, resolve }) =>
	paraglideMiddleware(event.request, ({ request, locale }) => {
		return resolve(
			{ ...event, request },
			{
				transformPageChunk: ({ html }) => html.replace('%lang%', locale)
			}
		);
	});

// Auth context: we no longer inject any user identity here. Connect
// calls forward the cairn_session cookie verbatim to the Go server
// which resolves it via SessionInterceptor. Pages that need the
// currently-logged-in user get it from +layout.server.ts via
// AuthService.GetCurrentUser.

export const handle: Handle = sequence(paraglideHandle);
