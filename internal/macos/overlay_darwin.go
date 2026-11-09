package macos

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// The overlay is a borderless panel that never becomes key or main, floats
// above other windows on every Space and lets clicks through, so showing it
// takes focus from nobody.
@interface RMOverlay : NSObject <WKNavigationDelegate>
@property (strong) NSPanel *panel;
@property (strong) WKWebView *web;
@property NSInteger want; // generation the panel should show; 0 = hidden
@property NSInteger loading;
@end

@implementation RMOverlay
- (void)webView:(WKWebView *)w didFinishNavigation:(WKNavigation *)n {
	if (self.want != 0 && self.want == self.loading) [self.panel orderFrontRegardless];
}
- (void)webView:(WKWebView *)w didFailProvisionalNavigation:(WKNavigation *)n withError:(NSError *)e {
	NSLog(@"overlay: failed %@", e);
}
- (void)webViewWebContentProcessDidTerminate:(WKWebView *)w {
	NSLog(@"overlay: web content process ended");
}
@end

static RMOverlay *ov;
static NSInteger gen;

static void ov_setup(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
	ov = [RMOverlay new];
	NSRect r = NSMakeRect(0, 0, 100, 100);
	NSPanel *p = [[NSPanel alloc] initWithContentRect:r
		styleMask:NSWindowStyleMaskBorderless | NSWindowStyleMaskNonactivatingPanel
		backing:NSBackingStoreBuffered defer:NO];
	p.level = NSStatusWindowLevel;
	p.opaque = NO;
	p.backgroundColor = NSColor.clearColor;
	p.hasShadow = NO;
	p.ignoresMouseEvents = YES;
	p.hidesOnDeactivate = NO;
	p.collectionBehavior = NSWindowCollectionBehaviorCanJoinAllSpaces |
		NSWindowCollectionBehaviorFullScreenAuxiliary |
		NSWindowCollectionBehaviorStationary | NSWindowCollectionBehaviorIgnoresCycle;
	WKWebView *w = [[WKWebView alloc] initWithFrame:r configuration:[WKWebViewConfiguration new]];
	[w setValue:@NO forKey:@"drawsBackground"];
	w.navigationDelegate = ov;
	p.contentView = w;
	ov.panel = p;
	ov.web = w;
}

// ov_show loads page and shows it centered on the screen under the mouse.
static void ov_show(char *page, int width, int height) {
	NSString *s = [NSString stringWithUTF8String:page];
	free(page);
	dispatch_async(dispatch_get_main_queue(), ^{
		NSPoint m = [NSEvent mouseLocation];
		NSScreen *screen = NSScreen.mainScreen;
		for (NSScreen *sc in NSScreen.screens) {
			if (NSMouseInRect(m, sc.frame, NO)) screen = sc;
		}
		NSRect f = screen.visibleFrame;
		NSRect r = NSMakeRect(NSMidX(f) - width / 2.0, NSMidY(f) - height / 2.0, width, height);
		[ov.panel setFrame:r display:NO];
		ov.want = ++gen;
		ov.loading = gen;
		[ov.web loadHTMLString:s baseURL:nil];
	});
}

static void ov_hide(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		ov.want = 0;
		[ov.panel orderOut:nil];
	});
}

static void ov_run(void) { [NSApp run]; }
*/
import "C"

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// OverlayCommand is one line of the overlay helper's input.
type OverlayCommand struct {
	HTML   string `json:"html,omitempty"` // show this page; empty hides the overlay
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// RunOverlay runs the keys overlay window, reading OverlayCommand JSON lines
// from in until it closes, then exits. AppKit needs it on the main thread:
// call it from the main goroutine, which main locks to that thread.
func RunOverlay(in io.Reader) {
	C.ov_setup()
	go func() {
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 1<<20), 8<<20)
		for sc.Scan() {
			var c OverlayCommand
			if json.Unmarshal(sc.Bytes(), &c) != nil {
				continue
			}
			if c.HTML == "" {
				C.ov_hide()
				continue
			}
			C.ov_show(C.CString(c.HTML), C.int(c.Width), C.int(c.Height))
		}
		os.Exit(0) // the daemon went away
	}()
	C.ov_run()
}
