#ifndef SPYGLASS_NATIVE_DARWIN_H
#define SPYGLASS_NATIVE_DARWIN_H

#include <stdbool.h>

// Returns true when the process runs inside an app bundle, which the
// UserNotifications framework requires.
bool spyglass_notify_available(void);

// Sets the notification delegate (for click-to-open) and requests
// notification permission. Call once at startup, before any notifications.
void spyglass_notify_init(void);

// Posts a notification. url, carried in userInfo, is opened when the
// notification is clicked.
void spyglass_notify(const char *ident, const char *title,
                     const char *subtitle, const char *body, const char *url);

#endif
