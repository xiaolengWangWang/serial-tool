#import <Cocoa/Cocoa.h>
#include <stdio.h>

// Exercise real AppKit geometry without Accessibility or Screen Recording permission.
static BOOL LayoutItem(NSView *view) {
    return ([view isKindOfClass:NSControl.class] && ![view isKindOfClass:NSTableView.class]) ||
        [view isKindOfClass:NSStackView.class] || [view isKindOfClass:NSScrollView.class] ||
        [view isKindOfClass:NSTabView.class];
}

static void CheckView(NSView *view, NSMutableArray *errors) {
    if (view.hidden) return;
    if ([view isKindOfClass:NSTabView.class]) {
        CheckView([(NSTabView *)view selectedTabViewItem].view, errors);
        return;
    }
    NSArray *children = view.subviews;
    for (NSUInteger i = 0; i < children.count; i++) {
        NSView *a = children[i];
        if (a.hidden) continue;
        if (LayoutItem(a)) {
            if (!NSContainsRect(NSInsetRect(view.bounds, -1, -1), [a alignmentRectForFrame:a.frame]))
                [errors addObject:[NSString stringWithFormat:@"clipped %@ %@ in %@", a, NSStringFromRect(a.frame), NSStringFromRect(view.bounds)]];
            for (NSUInteger j = i + 1; j < children.count; j++) {
                NSView *b = children[j];
                if (!b.hidden && LayoutItem(b) && NSIntersectsRect([a alignmentRectForFrame:a.frame], [b alignmentRectForFrame:b.frame]))
                    [errors addObject:[NSString stringWithFormat:@"overlap %@ %@ with %@ %@", a, NSStringFromRect(a.frame), b, NSStringFromRect(b.frame)]];
            }
        }
        if (![a isKindOfClass:NSControl.class] && ![a isKindOfClass:NSScrollView.class]) CheckView(a, errors);
    }
}

int RunLayoutChecks(id delegate, NSString *directory) {
    NSWindow *window = [delegate valueForKey:@"window"];
    NSPopUpButton *mode = [delegate valueForKey:@"mode"];
    NSTabView *send = [delegate valueForKey:@"sendView"];
    NSMutableArray *errors = [NSMutableArray array];
    NSArray *sizes = @[@[@1280, @700], @[@1600, @900], @[@1920, @1080], @[@1280, @700]];
    for (NSArray *size in sizes) {
        // Resize the actual content tree even when the display is smaller than the test size.
        window.contentView.frame = NSMakeRect(0, 0, [size[0] doubleValue], [size[1] doubleValue]);
        [delegate performSelector:@selector(layoutMainPanes)];
        for (NSString *name in mode.itemTitles) {
            [mode selectItemWithTitle:name];
            [delegate performSelector:@selector(modeChanged:) withObject:nil];
            for (NSTabViewItem *tab in send.tabViewItems) {
                [send selectTabViewItem:tab];
                [window.contentView layoutSubtreeIfNeeded];
                CheckView(window.contentView, errors);
            }
        }
        [send selectTabViewItemAtIndex:0];
        [mode selectItemAtIndex:0];
        [delegate performSelector:@selector(modeChanged:) withObject:nil];
        [window.contentView layoutSubtreeIfNeeded];
        [window orderFront:nil];
        [window display];
        [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.1]];
        NSBitmapImageRep *rep = [window.contentView bitmapImageRepForCachingDisplayInRect:window.contentView.bounds];
        [window.contentView cacheDisplayInRect:window.contentView.bounds toBitmapImageRep:rep];
        [[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:[directory stringByAppendingPathComponent:[NSString stringWithFormat:@"layout-%@x%@.png", size[0], size[1]]] atomically:YES];
    }
    for (NSString *error in [NSOrderedSet orderedSetWithArray:errors]) fprintf(stderr, "%s\n", error.UTF8String);
    fprintf(stderr, "Layout check: %lu failures (4 sizes x 5 modes x 2 send tabs)\n", (unsigned long)errors.count);
    return errors.count ? 1 : 0;
}
