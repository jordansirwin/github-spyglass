#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

#include "native_darwin.h"

@interface SpyglassNotifyDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation SpyglassNotifyDelegate

// Clicking a notification opens the URL carried in its userInfo.
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
    didReceiveNotificationResponse:(UNNotificationResponse *)response
             withCompletionHandler:(void (^)(void))completionHandler {
    NSString *url = response.notification.request.content.userInfo[@"url"];
    if (url != nil && url.length > 0) {
        [[NSWorkspace sharedWorkspace] openURL:[NSURL URLWithString:url]];
    }
    completionHandler();
}

// Show notifications even when the app is considered foreground.
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:
             (void (^)(UNNotificationPresentationOptions))completionHandler {
    completionHandler(UNNotificationPresentationOptionBanner |
                      UNNotificationPresentationOptionSound |
                      UNNotificationPresentationOptionList);
}

@end

static SpyglassNotifyDelegate *spyglassDelegate = nil;

bool spyglass_notify_available(void) {
    return [[NSBundle mainBundle] bundleIdentifier] != nil;
}

void spyglass_notify_init(void) {
    UNUserNotificationCenter *center =
        [UNUserNotificationCenter currentNotificationCenter];
    spyglassDelegate = [SpyglassNotifyDelegate new];
    center.delegate = spyglassDelegate;
    [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert |
                                             UNAuthorizationOptionSound)
                          completionHandler:^(BOOL granted,
                                              NSError *_Nullable error){
                          }];
}

void spyglass_notify(const char *ident, const char *title,
                     const char *subtitle, const char *body, const char *url) {
    UNMutableNotificationContent *content = [UNMutableNotificationContent new];
    content.title = [NSString stringWithUTF8String:title];
    if (subtitle != NULL && subtitle[0] != '\0') {
        content.subtitle = [NSString stringWithUTF8String:subtitle];
    }
    content.body = [NSString stringWithUTF8String:body];
    content.sound = [UNNotificationSound defaultSound];
    if (url != NULL && url[0] != '\0') {
        content.userInfo = @{@"url" : [NSString stringWithUTF8String:url]};
    }
    UNNotificationRequest *request = [UNNotificationRequest
        requestWithIdentifier:[NSString stringWithUTF8String:ident]
                      content:content
                      trigger:nil];
    [[UNUserNotificationCenter currentNotificationCenter]
        addNotificationRequest:request
         withCompletionHandler:^(NSError *_Nullable error){
         }];
}
