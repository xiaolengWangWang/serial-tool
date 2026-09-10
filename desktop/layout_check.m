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
    CheckFilters(delegate, errors);
    if ([[delegate valueForKey:@"analysisVisible"] boolValue]) [errors addObject:@"analysis center must start hidden"];
    if (![delegate respondsToSelector:@selector(databaseAnalysisDialog:)]) {
        [errors addObject:@"database analysis options dialog is missing"];
    } else {
        NSAlert *dialog = [delegate performSelector:@selector(databaseAnalysisDialog:) withObject:@[@"serial-data-test.sqlite3"]];
        [dialog layout];
        [dialog.window.contentView layoutSubtreeIfNeeded];
        CheckView(dialog.accessoryView, errors);
        NSPopUpButton *files = (NSPopUpButton *)[dialog.accessoryView viewWithTag:101];
        if (![files.titleOfSelectedItem isEqualToString:@"serial-data-test.sqlite3"]) [errors addObject:@"database selection missing"];
        NSBitmapImageRep *rep = [dialog.accessoryView bitmapImageRepForCachingDisplayInRect:dialog.accessoryView.bounds];
        [dialog.accessoryView cacheDisplayInRect:dialog.accessoryView.bounds toBitmapImageRep:rep];
        [[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:[directory stringByAppendingPathComponent:@"database-options.png"] atomically:YES];
    }
    NSUInteger cases = 0;
    NSArray *sizes = @[@[@1040, @700], @[@1280, @700], @[@1600, @900], @[@1920, @1080], @[@1280, @700]];
    for (NSNumber *visible in @[@NO, @YES]) {
    for (NSArray *size in sizes) {
        [delegate setValue:visible forKey:@"analysisVisible"];
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
                NSTableView *table = [delegate valueForKey:@"dataTable"];
                if (table.enclosingScrollView.contentSize.height < 200) [errors addObject:@"data viewport too short"];
                cases++;
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
        [[rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}] writeToFile:[directory stringByAppendingPathComponent:[NSString stringWithFormat:@"layout-%@x%@-analysis%@.png", size[0], size[1], visible]] atomically:YES];
    }
    }
    for (NSString *error in [NSOrderedSet orderedSetWithArray:errors]) fprintf(stderr, "%s\n", error.UTF8String);
    fprintf(stderr, "Layout check: %lu failures (%lu window/mode/tab cases, hidden and shown analysis)\n", (unsigned long)errors.count, (unsigned long)cases);
    return errors.count ? 1 : 0;
}
