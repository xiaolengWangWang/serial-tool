#import <Cocoa/Cocoa.h>
#include <stdio.h>

// Exercise real AppKit geometry without Accessibility or Screen Recording permission.
static BOOL LayoutItem(NSView *view) {
    return ([view isKindOfClass:NSControl.class] && ![view isKindOfClass:NSTableView.class]) ||
        [view isKindOfClass:NSStackView.class] || [view isKindOfClass:NSScrollView.class] ||
        [view isKindOfClass:NSTabView.class] ||
        ([NSStringFromClass(view.class) isEqualToString:@"ConnectionCard"] && [view.superview isKindOfClass:NSStackView.class]);
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

// Alert text and accessory controls can overlap across different containers.
static void AlertControls(NSView *view, NSMutableArray *controls) {
    if (view.hidden) return;
    // Native overlay scrollers intentionally overlap their document view.
    if ([view isKindOfClass:NSScrollView.class]) { [controls addObject:view]; return; }
    if ([view isKindOfClass:NSControl.class]) {
        if (![view isKindOfClass:NSImageView.class]) [controls addObject:view];
        return;
    }
    for (NSView *child in view.subviews) AlertControls(child, controls);
}

static void CheckAlert(NSAlert *dialog, NSMutableArray *errors) {
    NSView *root = dialog.window.contentView;
    NSMutableArray *controls = [NSMutableArray array];
    AlertControls(root, controls);
    for (NSUInteger i = 0; i < controls.count; i++) {
        NSView *a = controls[i];
        NSRect ra = [root convertRect:[a alignmentRectForFrame:a.frame] fromView:a.superview];
        for (NSUInteger j = i + 1; j < controls.count; j++) {
            NSView *b = controls[j];
            NSRect rb = [root convertRect:[b alignmentRectForFrame:b.frame] fromView:b.superview];
            if (NSIntersectsRect(ra, rb)) [errors addObject:[NSString stringWithFormat:@"alert overlap %@ %@ with %@ %@", a, NSStringFromRect(ra), b, NSStringFromRect(rb)]];
        }
    }
    CheckView(root, errors);
}

// A filter must retain the selected packet, never transfer selection to a new row.
static void CheckFilters(id delegate, NSMutableArray *errors) {
    NSMutableArray *packets = [delegate valueForKey:@"packets"];
    NSMutableArray *visible = [delegate valueForKey:@"visiblePackets"];
    NSTableView *table = [delegate valueForKey:@"dataTable"];
    NSDictionary *rx = @{@"ts": @"12:00:00", @"dir": @"RX", @"hex": @"41", @"ascii": @"A", @"kind": @"ASCII", @"rawLen": @1, @"len": @"1 B", @"epoch": @([NSDate date].timeIntervalSince1970)};
    NSDictionary *tx = @{@"ts": @"11:00:00", @"dir": @"TX", @"hex": @"FF 00", @"ascii": @"..", @"kind": @"HEX", @"rawLen": @9, @"len": @"9 B", @"epoch": @([NSDate date].timeIntervalSince1970 - 3600)};
    [packets addObjectsFromArray:@[rx, tx]];
    [delegate performSelector:@selector(clearFilter)];
    [table selectRowIndexes:[NSIndexSet indexSetWithIndex:0] byExtendingSelection:NO];
    NSPopUpButton *direction = [delegate valueForKey:@"dirFilter"];
    [direction selectItemWithTitle:@"TX"];
    [delegate performSelector:@selector(applyFilter)];
    if (![visible isEqualToArray:@[tx]]) [errors addObject:@"direction filter failed"];
    if (table.selectedRowIndexes.count) [errors addObject:@"filter transferred selection from RX to TX"];
    [table selectRowIndexes:[NSIndexSet indexSetWithIndex:0] byExtendingSelection:NO];
    [delegate performSelector:@selector(clearFilter)];
    if (table.selectedRow != 1) [errors addObject:@"reset lost selected TX identity"];
    [delegate performSelector:@selector(applyFilter)];
    if (table.selectedRow != 1) [errors addObject:@"refresh lost selected TX identity"];
    for (NSString *key in @[@"typeFilter", @"lengthFilter", @"timeFilter"]) {
        [[delegate valueForKey:key] selectItemAtIndex:1];
        [delegate performSelector:@selector(applyFilter)];
        if (![visible isEqualToArray:@[rx]]) [errors addObject:[@"filter failed: " stringByAppendingString:key]];
        [delegate performSelector:@selector(clearFilter)];
    }
    NSTextField *search = [delegate valueForKey:@"searchField"];
    search.stringValue = @"ff";
    [delegate performSelector:@selector(controlTextDidChange:) withObject:[NSNotification notificationWithName:NSControlTextDidChangeNotification object:search]];
    if (![visible isEqualToArray:@[tx]]) [errors addObject:@"live HEX search failed"];
    [delegate performSelector:@selector(selectAllPackets:) withObject:nil];
    [[delegate valueForKey:@"analysisScope"] selectItemWithTitle:@"选中数据"];
    if (![[delegate performSelector:@selector(analysisPackets)] isEqualToArray:@[tx]]) [errors addObject:@"select all analysis included hidden packets"];
    [delegate performSelector:@selector(invertPacketSelection:) withObject:nil];
    if ([[delegate performSelector:@selector(analysisPackets)] count]) [errors addObject:@"invert selection failed"];
    [delegate performSelector:@selector(clearFilter)];
    [delegate performSelector:@selector(selectAllPackets:) withObject:nil];
    [delegate performSelector:@selector(analyzeSelected:) withObject:nil];
    NSTextView *report = [delegate valueForKey:@"analysisResult"];
    if (![report.string containsString:@"HEX：41"] || ![report.string containsString:@"HEX：FF 00"])
        [errors addObject:@"selected analysis failed to analyze both selected packets in analysis center"];
    report.string = @"选择分析范围后，点击“开始本地分析”。\n\nAI 增强分析默认关闭。";
    [delegate setValue:@NO forKey:@"analysisVisible"];
    [[delegate valueForKey:@"analysisScope"] selectItemAtIndex:0];
    [packets removeAllObjects];
    [delegate performSelector:@selector(clearFilter)];
}

int RunLayoutChecks(id delegate, NSString *directory) {
    NSWindow *window = [delegate valueForKey:@"window"];
    NSPopUpButton *mode = [delegate valueForKey:@"mode"];
    NSTabView *send = [delegate valueForKey:@"sendView"];
    NSMutableArray *errors = [NSMutableArray array];
    if (![delegate respondsToSelector:@selector(applyConnectionStats:)]) {
        [errors addObject:@"structured connection status missing"];
    } else {
        NSDictionary *snapshot = @{@"state": @2, @"mode": @"TCP 服务端", @"listening": @YES,
            @"endpoint": @"0.0.0.0:9000", @"rx": @"71 条 · 426 B", @"tx": @"80 条 · 480 B",
            @"elapsed": @"00:02:32", @"reconnects": @0, @"errors": @0,
            @"peers": @[@"192.168.1.100:54321"], @"peer_count": @1};
        [delegate performSelector:@selector(applyConnectionStats:) withObject:snapshot];
        if (![[[delegate valueForKey:@"status"] stringValue] containsString:@"监听中"]) [errors addObject:@"server mislabeled as connected"];
        if (![[[delegate valueForKey:@"connectionMetrics"] stringValue] containsString:@"426 B"]) [errors addObject:@"receive statistics missing"];
        if (![[[delegate valueForKey:@"peerInfo"] stringValue] containsString:@"192.168.1.100:54321"]) [errors addObject:@"peer information missing"];
        NSMutableDictionary *changed = [[snapshot mutableCopy] autorelease];
        changed[@"peers"] = @[]; changed[@"peer_count"] = @0;
        [delegate performSelector:@selector(applyConnectionStats:) withObject:changed];
        if (![[[delegate valueForKey:@"peerInfo"] stringValue] containsString:@"等待客户端"]) [errors addObject:@"empty listener state missing"];
        changed[@"listening"] = @NO; changed[@"datagram"] = @YES; changed[@"mode"] = @"串口服务器";
        [delegate performSelector:@selector(applyConnectionStats:) withObject:changed];
        if (![[[delegate valueForKey:@"status"] stringValue] containsString:@"就绪"] || ![[[delegate valueForKey:@"peerInfo"] stringValue] containsString:@"无连接"])
            [errors addObject:@"UDP bridge incorrectly implies remote connection"];
        changed[@"datagram"] = @NO; changed[@"mode"] = @"HTTP 客户端";
        [delegate performSelector:@selector(applyConnectionStats:) withObject:changed];
        if (![[[delegate valueForKey:@"peerInfo"] stringValue] containsString:@"不代表服务可达"]) [errors addObject:@"HTTP readiness misleading"];
        changed[@"state"] = @5;
        [delegate performSelector:@selector(applyConnectionStats:) withObject:changed];
        if (![[[delegate valueForKey:@"status"] stringValue] containsString:@"失败"]) [errors addObject:@"connection error not shown"];
        changed[@"state"] = @0;
        [delegate performSelector:@selector(applyConnectionStats:) withObject:changed];
        if (![[[delegate valueForKey:@"status"] stringValue] containsString:@"未连接"] || [[[delegate valueForKey:@"peerInfo"] stringValue] containsString:@"192.168.1.100"])
            [errors addObject:@"disconnect retained stale peer"];
    }
    CheckFilters(delegate, errors);
    if ([[delegate valueForKey:@"analysisVisible"] boolValue]) [errors addObject:@"analysis center must start hidden"];
    if (![delegate respondsToSelector:@selector(databaseAnalysisDialog:)]) {
        [errors addObject:@"database analysis options dialog is missing"];
    } else {
        for (NSNumber *width in @[@1040, @1280, @1600]) {
        [window setContentSize:NSMakeSize(width.doubleValue, 700)];
        NSAlert *dialog = [delegate performSelector:@selector(databaseAnalysisDialog:) withObject:@[@"serial-data-first.sqlite3", @"serial-data-test.sqlite3"]];
        [window orderFront:nil];
        [dialog beginSheetModalForWindow:window completionHandler:nil];
        [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.2]];
        [dialog.window.contentView layoutSubtreeIfNeeded];
        CheckAlert(dialog, errors);
        id files = [dialog.accessoryView viewWithTag:101];
        if (![files respondsToSelector:@selector(selectedFilenames)]) {
            [errors addObject:@"database multiple file selection missing"];
        } else {
            if (![[files performSelector:@selector(selectedFilenames)] isEqualToArray:@[@"serial-data-test.sqlite3"]]) [errors addObject:@"default database selection missing"];
            NSTableView *table = [(NSScrollView *)files documentView];
            [table selectAll:nil];
            if ([[files performSelector:@selector(selectedFilenames)] count] != 2) [errors addObject:@"database select all failed"];
            [table deselectAll:nil];
            if ([[files performSelector:@selector(selectedFilenames)] count]) [errors addObject:@"database clear selection failed"];
        }
        NSPopUpButton *count = (NSPopUpButton *)[dialog.accessoryView viewWithTag:105];
        if ([count.lastItem.representedObject integerValue] != 1000000) [errors addObject:@"database limit cannot select one million"];
        [count selectItem:count.lastItem];
        CheckAlert(dialog, errors);
        [dialog.window display];
        NSView *root = dialog.window.contentView;
        NSBitmapImageRep *rep = [root bitmapImageRepForCachingDisplayInRect:root.bounds];
        [root cacheDisplayInRect:root.bounds toBitmapImageRep:rep];
        [[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:[directory stringByAppendingPathComponent:[NSString stringWithFormat:@"database-dialog-%@.png", width]] atomically:YES];
        [window endSheet:dialog.window returnCode:NSAlertSecondButtonReturn];
        [dialog.window orderOut:nil];
        }
    }
    NSUInteger cases = 0;
    NSArray *sizes = @[@[@1040, @700], @[@1280, @700], @[@1280, @820], @[@1600, @900], @[@1920, @1080], @[@1280, @700]];
    for (NSString *appearance in @[NSAppearanceNameAqua, NSAppearanceNameDarkAqua]) {
    window.appearance = [NSAppearance appearanceNamed:appearance];
    for (NSNumber *visible in @[@NO, @YES]) {
    for (NSArray *size in sizes) {
        [delegate setValue:visible forKey:@"analysisVisible"];
        // Resize the actual content tree even when the display is smaller than the test size.
        window.contentView.frame = NSMakeRect(0, 0, [size[0] doubleValue], [size[1] doubleValue]);
        [delegate performSelector:@selector(layoutMainPanes)];
        for (NSString *name in mode.itemTitles) {
            [mode selectItemWithTitle:name];
            for (NSString *role in @[@"服务端", @"客户端"]) {
            [[delegate valueForKey:@"role"] selectItemWithTitle:role];
            [delegate performSelector:@selector(modeChanged:) withObject:nil];
            for (NSTabViewItem *tab in send.tabViewItems) {
                [send selectTabViewItem:tab];
                [window.contentView layoutSubtreeIfNeeded];
                CheckView(window.contentView, errors);
                NSScrollView *connections = [delegate valueForKey:@"connectionScroll"];
                NSStackView *cards = [delegate valueForKey:@"connectionCards"];
                CheckView(connections.documentView, errors);
                if (fabs(cards.frame.size.width - (connections.contentSize.width - 20)) > 1)
                    [errors addObject:@"connection cards do not fill sidebar width"];
                NSRect visibleCards = [connections.contentView convertRect:cards.bounds fromView:cards];
                if (fabs(NSMaxY(connections.contentView.bounds) - NSMaxY(visibleCards) - 10) > 1)
                    [errors addObject:[NSString stringWithFormat:@"connection cards not top aligned: %@ clip %@", NSStringFromRect(visibleCards), NSStringFromRect(connections.contentView.bounds)]];
                NSTableView *table = [delegate valueForKey:@"dataTable"];
                if (table.enclosingScrollView.contentSize.height < 200) [errors addObject:@"data viewport too short"];
                cases++;
            }
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
        [[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:[directory stringByAppendingPathComponent:[NSString stringWithFormat:@"layout-%@x%@-analysis%@-%@.png", size[0], size[1], visible, appearance]] atomically:YES];
    }
    }
    }
    for (NSString *error in [NSOrderedSet orderedSetWithArray:errors]) fprintf(stderr, "%s\n", error.UTF8String);
    fprintf(stderr, "Layout check: %lu failures (%lu window/mode/tab cases, hidden and shown analysis)\n", (unsigned long)errors.count, (unsigned long)cases);
    return errors.count ? 1 : 0;
}
