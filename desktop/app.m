#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include "app.h"

@interface AppDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate> {
    NSWindow *_window;
    NSWindow *_monitorWindow;
    NSPopUpButton *_mode, *_bridgeProtocol, *_bridgeRole;
    NSComboBox *_ports, *_baud, *_data, *_stop, *_parity, *_eol;
    NSTextField *_address, *_bridgeAddress, *_endpointLabel, *_status, *_interval;
    NSArray *_serialControls, *_bridgeControls;
    NSButton *_refresh, *_connect, *_hexView, *_hexSend, *_timerButton;
    NSTextView *_log, *_send, *_monitorLog;
    NSTimer *_sendTimer;
    BOOL _connected;
    BOOL _monitorPaused;
}
- (void)appendText:(NSString *)text;
- (void)appendMonitorText:(NSString *)text;
- (NSString *)sendCurrentData;
- (void)stopTimer;
@end

static NSTextField *Label(NSString *text, NSRect frame) {
    NSTextField *label = [[[NSTextField alloc] initWithFrame:frame] autorelease];
    label.stringValue = text;
    label.editable = NO;
    label.selectable = NO;
    label.bordered = NO;
    label.drawsBackground = NO;
    return label;
}

static NSComboBox *Combo(NSRect frame, NSArray *items, NSString *value) {
    NSComboBox *box = [[[NSComboBox alloc] initWithFrame:frame] autorelease];
    [box addItemsWithObjectValues:items];
    box.stringValue = value;
    box.numberOfVisibleItems = 10;
    return box;
}

@implementation AppDelegate
- (void)applicationDidFinishLaunching:(NSNotification *)note {
    _window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1040, 700)
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
        backing:NSBackingStoreBuffered defer:NO];
    _window.title = @"Go 网络与串口工具";
    _window.contentMinSize = NSMakeSize(900, 700);
    _window.delegate = self;
    [_window center];
    NSView *view = _window.contentView;

    NSBox *config = [[[NSBox alloc] initWithFrame:NSMakeRect(20, 20, 280, 660)] autorelease];
    config.title = @"连接配置";
    config.autoresizingMask = NSViewHeightSizable;
    [view addSubview:config];

    NSTextField *modeLabel = Label(@"工作模式", NSMakeRect(40, 620, 100, 22));
    [view addSubview:modeLabel];
    _mode = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(40, 586, 240, 30) pullsDown:NO];
    [_mode addItemsWithTitles:@[@"串口", @"TCP 服务端", @"TCP 客户端", @"UDP 服务端", @"UDP 客户端", @"串口服务器"]];
    _mode.target = self; _mode.action = @selector(modeChanged:);
    [view addSubview:_mode];

    _endpointLabel = [Label(@"串口", NSMakeRect(40, 548, 150, 22)) retain];
    [view addSubview:_endpointLabel];
    _ports = [Combo(NSMakeRect(40, 514, 174, 30), @[], @"") retain];
    [view addSubview:_ports];
    _refresh = [[NSButton buttonWithTitle:@"刷新" target:self action:@selector(refresh:)] retain];
    _refresh.frame = NSMakeRect(220, 514, 60, 30); [view addSubview:_refresh];
    _address = [[NSTextField alloc] initWithFrame:NSMakeRect(40, 514, 240, 30)];
    _address.placeholderString = @"例如 192.168.1.10:9000";
    _address.hidden = YES; [view addSubview:_address];

    NSTextField *baudLabel = Label(@"波特率", NSMakeRect(40, 466, 100, 22)); [view addSubview:baudLabel];
    NSTextField *dataLabel = Label(@"数据位", NSMakeRect(165, 466, 100, 22)); [view addSubview:dataLabel];
    _baud = [Combo(NSMakeRect(40, 432, 110, 30), @[@"1200",@"2400",@"4800",@"9600",@"19200",@"38400",@"57600",@"115200",@"230400",@"460800",@"921600"], @"115200") retain];
    _data = [Combo(NSMakeRect(165, 432, 115, 30), @[@"5",@"6",@"7",@"8"], @"8") retain];
    [view addSubview:_baud]; [view addSubview:_data];
    NSTextField *parityLabel = Label(@"校验位", NSMakeRect(40, 388, 100, 22)); [view addSubview:parityLabel];
    NSTextField *stopLabel = Label(@"停止位", NSMakeRect(165, 388, 100, 22)); [view addSubview:stopLabel];
    _parity = [Combo(NSMakeRect(40, 354, 110, 30), @[@"无校验",@"奇校验",@"偶校验"], @"无校验") retain];
    _stop = [Combo(NSMakeRect(165, 354, 115, 30), @[@"1",@"2"], @"1") retain];
    [view addSubview:_parity]; [view addSubview:_stop];
    _serialControls = [[NSArray alloc] initWithObjects:baudLabel, dataLabel, parityLabel, stopLabel, _baud, _data, _parity, _stop, nil];

    NSTextField *protocolLabel = Label(@"网络协议", NSMakeRect(40, 310, 100, 22)); [view addSubview:protocolLabel];
    NSTextField *roleLabel = Label(@"网络角色", NSMakeRect(165, 310, 100, 22)); [view addSubview:roleLabel];
    _bridgeProtocol = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(40, 278, 110, 30) pullsDown:NO];
    [_bridgeProtocol addItemsWithTitles:@[@"TCP", @"UDP"]]; [view addSubview:_bridgeProtocol];
    _bridgeRole = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(165, 278, 115, 30) pullsDown:NO];
    [_bridgeRole addItemsWithTitles:@[@"服务端", @"客户端"]];
    _bridgeRole.target = self; _bridgeRole.action = @selector(bridgeOptionsChanged:); [view addSubview:_bridgeRole];
    NSTextField *bridgeAddressLabel = Label(@"网络地址", NSMakeRect(40, 242, 150, 22)); [view addSubview:bridgeAddressLabel];
    _bridgeAddress = [[NSTextField alloc] initWithFrame:NSMakeRect(40, 208, 240, 30)];
    _bridgeAddress.placeholderString = @"例如 :9000"; [view addSubview:_bridgeAddress];
    _bridgeControls = [[NSArray alloc] initWithObjects:protocolLabel, roleLabel, bridgeAddressLabel,
                        _bridgeProtocol, _bridgeRole, _bridgeAddress, nil];
    for (NSView *control in _bridgeControls) control.hidden = YES;

    _status = [Label(@"● 未连接", NSMakeRect(40, 158, 240, 24)) retain];
    _status.alignment = NSTextAlignmentCenter;
    _status.textColor = NSColor.secondaryLabelColor;
    [view addSubview:_status];
    _connect = [[NSButton buttonWithTitle:@"连接" target:self action:@selector(toggleConnect:)] retain];
    _connect.frame = NSMakeRect(40, 112, 240, 36); _connect.keyEquivalent = @"\r";
    [view addSubview:_connect];
    NSTextField *addressHint = Label(@"服务端绑定本机地址，客户端填写远程地址", NSMakeRect(40, 34, 240, 22));
    addressHint.font = [NSFont systemFontOfSize:11]; addressHint.textColor = NSColor.tertiaryLabelColor;
    addressHint.alignment = NSTextAlignmentCenter; [view addSubview:addressHint];

    NSTextField *receiveTitle = Label(@"接收数据", NSMakeRect(320, 655, 120, 24));
    receiveTitle.font = [NSFont boldSystemFontOfSize:14]; [view addSubview:receiveTitle];
    _hexView = [[NSButton checkboxWithTitle:@"HEX 显示" target:self action:@selector(hexViewChanged:)] retain];
    _hexView.frame = NSMakeRect(590, 653, 100, 26); _hexView.autoresizingMask = NSViewMinXMargin; [view addSubview:_hexView];
    NSButton *monitor = [NSButton buttonWithTitle:@"监控窗口" target:self action:@selector(openMonitor:)];
    monitor.frame = NSMakeRect(700, 652, 110, 28); monitor.autoresizingMask = NSViewMinXMargin; [view addSubview:monitor];
    NSButton *export = [NSButton buttonWithTitle:@"导出" target:self action:@selector(exportLog:)];
    export.frame = NSMakeRect(820, 652, 90, 28); export.autoresizingMask = NSViewMinXMargin; [view addSubview:export];
    NSButton *clear = [NSButton buttonWithTitle:@"清空" target:self action:@selector(clear:)];
    clear.frame = NSMakeRect(930, 652, 90, 28); clear.autoresizingMask = NSViewMinXMargin; [view addSubview:clear];

    NSScrollView *logScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(320, 280, 700, 365)] autorelease];
    logScroll.borderType = NSBezelBorder; logScroll.hasVerticalScroller = YES;
    logScroll.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    _log = [[NSTextView alloc] initWithFrame:logScroll.contentView.bounds];
    _log.editable = NO; _log.font = [NSFont monospacedSystemFontOfSize:13 weight:NSFontWeightRegular];
    _log.autoresizingMask = NSViewWidthSizable; logScroll.documentView = _log; [view addSubview:logScroll];

    NSTextField *sendTitle = Label(@"发送数据", NSMakeRect(320, 242, 120, 24));
    sendTitle.font = [NSFont boldSystemFontOfSize:14]; [view addSubview:sendTitle];
    _hexSend = [[NSButton checkboxWithTitle:@"HEX 发送" target:nil action:nil] retain];
    _hexSend.frame = NSMakeRect(590, 240, 100, 26); _hexSend.autoresizingMask = NSViewMinXMargin; [view addSubview:_hexSend];
    [view addSubview:Label(@"行尾", NSMakeRect(700, 242, 36, 24))];
    _eol = [Combo(NSMakeRect(740, 238, 90, 30), @[@"无",@"LF",@"CR",@"CRLF"], @"无") retain];
    _eol.autoresizingMask = NSViewMinXMargin; [view addSubview:_eol];
    [view addSubview:Label(@"间隔(ms)", NSMakeRect(842, 242, 64, 24))];
    _interval = [[NSTextField alloc] initWithFrame:NSMakeRect(912, 238, 108, 30)];
    _interval.stringValue = @"1000"; _interval.alignment = NSTextAlignmentRight;
    _interval.autoresizingMask = NSViewMinXMargin; [view addSubview:_interval];

    NSScrollView *sendScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(320, 58, 590, 172)] autorelease];
    sendScroll.borderType = NSBezelBorder; sendScroll.hasVerticalScroller = YES; sendScroll.autoresizingMask = NSViewWidthSizable;
    _send = [[NSTextView alloc] initWithFrame:sendScroll.contentView.bounds];
    _send.font = [NSFont monospacedSystemFontOfSize:13 weight:NSFontWeightRegular];
    _send.autoresizingMask = NSViewWidthSizable; sendScroll.documentView = _send; [view addSubview:sendScroll];
    NSButton *sendButton = [NSButton buttonWithTitle:@"发送一次" target:self action:@selector(send:)];
    sendButton.frame = NSMakeRect(925, 147, 95, 83); sendButton.autoresizingMask = NSViewMinXMargin; [view addSubview:sendButton];
    _timerButton = [[NSButton buttonWithTitle:@"开始定时" target:self action:@selector(toggleTimer:)] retain];
    _timerButton.frame = NSMakeRect(925, 58, 95, 83); _timerButton.autoresizingMask = NSViewMinXMargin; [view addSubview:_timerButton];
    NSTextField *hint = Label(@"HEX 示例：01 03 00 00 00 02", NSMakeRect(320, 24, 360, 24));
    hint.textColor = NSColor.secondaryLabelColor; [view addSubview:hint];

    [_window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [self refresh:nil];
    char *database = GoDatabaseInfo();
    NSString *databaseInfo = [NSString stringWithUTF8String:database ?: ""]; free(database);
    if ([databaseInfo hasPrefix:@"错误:"]) [self alert:databaseInfo];
    else [self appendText:[NSString stringWithFormat:@"[SQLite 数据目录：%@]\n", databaseInfo]];
}

- (NSString *)modeName { return _mode.titleOfSelectedItem; }
- (BOOL)isBridgeMode { return [[self modeName] isEqualToString:@"串口服务器"]; }
- (BOOL)isServerMode { return [self isBridgeMode] ? [_bridgeRole.titleOfSelectedItem isEqualToString:@"服务端"] : [[self modeName] hasSuffix:@"服务端"]; }
- (BOOL)isNetworkMode { return ![[self modeName] isEqualToString:@"串口"] && ![self isBridgeMode]; }

- (void)setStatus:(NSString *)text color:(NSColor *)color {
    _status.stringValue = text;
    _status.textColor = color;
}

- (void)refresh:(id)sender {
    char *raw = GoListPorts();
    NSString *value = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    [_ports removeAllItems];
    if ([value hasPrefix:@"错误:"]) { [self alert:value]; return; }
    NSArray *items = value.length ? [value componentsSeparatedByString:@"\n"] : @[];
    [_ports addItemsWithObjectValues:items];
    if (items.count) [_ports selectItemAtIndex:0];
}

- (void)toggleConnect:(id)sender {
    if (_connected) {
        [self stopTimer]; GoDisconnect(); _connected = NO; _mode.enabled = YES;
        [self setStatus:@"● 未连接" color:NSColor.secondaryLabelColor];
        [self modeChanged:nil]; [self appendText:@"\n[已停止]\n"]; return;
    }

    NSString *mode = [self modeName];
    BOOL network = [self isNetworkMode], bridge = [self isBridgeMode], server = [self isServerMode];
    NSString *serialName = _ports.stringValue;
    NSString *endpoint = bridge ? _bridgeAddress.stringValue : (network ? _address.stringValue : serialName);
    if ((bridge || !network) && !serialName.length) { [self alert:@"请选择串口"]; return; }
    if ((network || bridge) && !endpoint.length) { [self alert:@"请输入 IP:端口"]; return; }
    if ((network || bridge) && ![endpoint containsString:@":"]) {
        NSInteger port = endpoint.integerValue;
        if (port <= 0) { [self alert:@"地址格式应为 IP:端口，例如 192.168.1.10:9000"]; return; }
        endpoint = server ? [NSString stringWithFormat:@":%ld", (long)port]
                          : [NSString stringWithFormat:@"127.0.0.1:%ld", (long)port];
        if (bridge) _bridgeAddress.stringValue = endpoint; else _address.stringValue = endpoint;
    }

    BOOL hex = _hexView.state == NSControlStateValueOn;
    char *err;
    if (bridge) err = GoStartSerialServer((char *)serialName.UTF8String, _baud.intValue, _data.intValue, _stop.intValue,
                                           (char *)_parity.stringValue.UTF8String,
                                           (char *)_bridgeProtocol.titleOfSelectedItem.UTF8String,
                                           (char *)_bridgeRole.titleOfSelectedItem.UTF8String,
                                           (char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"TCP 服务端"]) err = GoListen((char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"TCP 客户端"]) err = GoConnectTCP((char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"UDP 服务端"]) err = GoListenUDP((char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"UDP 客户端"]) err = GoConnectUDP((char *)endpoint.UTF8String, hex);
    else err = GoConnect((char *)endpoint.UTF8String, _baud.intValue, _data.intValue, _stop.intValue,
                         (char *)_parity.stringValue.UTF8String, hex);
    NSString *message = [NSString stringWithUTF8String:err ?: ""]; free(err);
    if (message.length) { [self alert:message]; return; }

    _connected = YES; _mode.enabled = NO; _ports.enabled = NO; _address.enabled = NO; _bridgeAddress.enabled = NO;
    _refresh.enabled = NO; for (NSControl *control in _serialControls) control.enabled = NO;
    for (NSControl *control in _bridgeControls) control.enabled = NO;
    _connect.title = server ? @"停止监听" : @"断开";
    [self setStatus:server ? @"● 监听中" : @"● 已连接" color:NSColor.systemGreenColor];
    if (bridge)
        [self appendText:[NSString stringWithFormat:@"[串口服务器已启动：%@ ↔ %@ %@ %@]\n", serialName,
                          _bridgeProtocol.titleOfSelectedItem, _bridgeRole.titleOfSelectedItem, endpoint]];
    else
        [self appendText:server
            ? [NSString stringWithFormat:@"[正在监听 %@ %@]\n", mode, endpoint]
            : [NSString stringWithFormat:@"[已连接 %@ %@]\n", mode, endpoint]];
}

- (NSString *)sendCurrentData {
    NSString *mode = [self modeName];
    BOOL hex = _hexSend.state == NSControlStateValueOn;
    char *err;
    if ([mode hasPrefix:@"TCP"])
        err = GoNetworkSend((char *)_send.string.UTF8String, hex, (char *)_eol.stringValue.UTF8String);
    else if ([mode hasPrefix:@"UDP"])
        err = GoUDPSend((char *)_send.string.UTF8String, hex, (char *)_eol.stringValue.UTF8String);
    else
        err = GoSend((char *)_send.string.UTF8String, hex, (char *)_eol.stringValue.UTF8String);
    NSString *message = [NSString stringWithUTF8String:err ?: ""]; free(err);
    return message;
}

- (void)send:(id)sender {
    NSString *message = [self sendCurrentData];
    if (message.length) [self alert:message];
}

- (void)timerFired:(NSTimer *)timer {
    NSString *message = [self sendCurrentData];
    if (message.length) {
        [self stopTimer];
        [self alert:[@"定时发送已停止：" stringByAppendingString:message]];
    }
}

- (void)toggleTimer:(id)sender {
    if (_sendTimer) { [self stopTimer]; return; }
    if (!_connected) { [self alert:@"请先连接或开始监听"]; return; }
    if (!_send.string.length && (_hexSend.state == NSControlStateValueOn || [_eol.stringValue isEqualToString:@"无"])) {
        [self alert:@"请输入要发送的数据"]; return;
    }
    NSInteger milliseconds = _interval.integerValue;
    if (milliseconds < 10) { [self alert:@"定时间隔不能小于 10 ms"]; return; }
    _sendTimer = [NSTimer scheduledTimerWithTimeInterval:milliseconds / 1000.0 target:self
        selector:@selector(timerFired:) userInfo:nil repeats:YES];
    _interval.enabled = NO; _timerButton.title = @"停止定时";
    [self appendText:[NSString stringWithFormat:@"\n[已开始定时发送：%ld ms]\n", (long)milliseconds]];
}

- (void)stopTimer {
    if (!_sendTimer) return;
    [_sendTimer invalidate]; _sendTimer = nil;
    _interval.enabled = YES; _timerButton.title = @"开始定时";
    [self appendText:@"\n[已停止定时发送]\n"];
}

- (void)hexViewChanged:(id)sender { GoSetHexView(_hexView.state == NSControlStateValueOn); }
- (void)bridgeOptionsChanged:(id)sender {
    BOOL server = [self isServerMode];
    _bridgeAddress.stringValue = server ? @":9000" : @"127.0.0.1:9000";
    _connect.title = server ? @"启动服务器" : @"连接并启动";
}
- (void)modeChanged:(id)sender {
    BOOL network = [self isNetworkMode], bridge = [self isBridgeMode], server = [self isServerMode];
    _ports.hidden = network; _refresh.hidden = network; _address.hidden = !network;
    for (NSView *control in _serialControls) control.hidden = network;
    for (NSView *control in _bridgeControls) control.hidden = !bridge;
    _endpointLabel.stringValue = network ? (server ? @"本机监听地址" : @"远程服务器地址") : @"串口";
    _connect.title = bridge ? (server ? @"启动服务器" : @"连接并启动") : (server ? @"开始监听" : @"连接");
    if (sender && network) _address.stringValue = server ? @":9000" : @"127.0.0.1:9000";
    if (sender && bridge) _bridgeAddress.stringValue = server ? @":9000" : @"127.0.0.1:9000";
    if (!network) [self refresh:nil];
    _ports.enabled = !_connected; _address.enabled = !_connected; _bridgeAddress.enabled = !_connected;
    _refresh.enabled = !_connected; for (NSControl *control in _serialControls) control.enabled = !_connected;
    for (NSControl *control in _bridgeControls) control.enabled = !_connected;
}

- (void)openMonitor:(id)sender {
    if (!_monitorWindow) {
        _monitorWindow = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 800, 500)
            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
            backing:NSBackingStoreBuffered defer:NO];
        _monitorWindow.title = @"实时数据监控";
        _monitorWindow.contentMinSize = NSMakeSize(800, 500);
        _monitorWindow.releasedWhenClosed = NO;
        NSView *view = _monitorWindow.contentView;
        NSTextField *hint = Label(@"仅显示实时接收数据", NSMakeRect(20, 464, 220, 24));
        hint.textColor = NSColor.secondaryLabelColor; [view addSubview:hint];
        NSButton *database = [NSButton buttonWithTitle:@"数据库位置" target:self action:@selector(revealDatabase:)];
        database.frame = NSMakeRect(350, 460, 120, 30); database.autoresizingMask = NSViewMinXMargin; [view addSubview:database];
        NSButton *pause = [NSButton buttonWithTitle:@"暂停滚动" target:self action:@selector(toggleMonitorPause:)];
        pause.frame = NSMakeRect(480, 460, 100, 30); pause.autoresizingMask = NSViewMinXMargin; [view addSubview:pause];
        NSButton *clear = [NSButton buttonWithTitle:@"清空" target:self action:@selector(clearMonitor:)];
        clear.frame = NSMakeRect(590, 460, 90, 30); clear.autoresizingMask = NSViewMinXMargin; [view addSubview:clear];
        NSButton *export = [NSButton buttonWithTitle:@"导出" target:self action:@selector(exportMonitor:)];
        export.frame = NSMakeRect(690, 460, 90, 30); export.autoresizingMask = NSViewMinXMargin; [view addSubview:export];
        NSScrollView *scroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(20, 20, 760, 430)] autorelease];
        scroll.borderType = NSBezelBorder; scroll.hasVerticalScroller = YES;
        scroll.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        _monitorLog = [[NSTextView alloc] initWithFrame:scroll.contentView.bounds];
        _monitorLog.editable = NO; _monitorLog.font = [NSFont monospacedSystemFontOfSize:13 weight:NSFontWeightRegular];
        _monitorLog.autoresizingMask = NSViewWidthSizable; scroll.documentView = _monitorLog; [view addSubview:scroll];
        [_monitorWindow center];
    }
    [_monitorWindow makeKeyAndOrderFront:nil];
}

- (void)toggleMonitorPause:(NSButton *)sender {
    _monitorPaused = !_monitorPaused;
    sender.title = _monitorPaused ? @"继续滚动" : @"暂停滚动";
    if (!_monitorPaused) [_monitorLog scrollRangeToVisible:NSMakeRange(_monitorLog.string.length, 0)];
}

- (void)clearMonitor:(id)sender { [_monitorLog setString:@""]; }
- (void)revealDatabase:(id)sender {
    char *raw = GoDatabaseInfo();
    NSString *path = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    if ([path hasPrefix:@"错误:"]) { [self alert:path]; return; }
    [[NSWorkspace sharedWorkspace] activateFileViewerSelectingURLs:@[[NSURL fileURLWithPath:path]]];
}
- (void)appendMonitorText:(NSString *)text {
    if (!_monitorLog) return;
    [_monitorLog.textStorage appendAttributedString:[[[NSAttributedString alloc] initWithString:text] autorelease]];
    if (!_monitorPaused) [_monitorLog scrollRangeToVisible:NSMakeRange(_monitorLog.string.length, 0)];
}

- (void)clear:(id)sender { [_log setString:@""]; }
- (void)saveText:(NSString *)text prefix:(NSString *)prefix window:(NSWindow *)window {
    NSSavePanel *panel = [NSSavePanel savePanel];
    NSDateFormatter *formatter = [[[NSDateFormatter alloc] init] autorelease];
    formatter.dateFormat = @"yyyyMMdd-HHmmss";
    panel.nameFieldStringValue = [NSString stringWithFormat:@"%@-%@.txt", prefix, [formatter stringFromDate:[NSDate date]]];
    [panel beginSheetModalForWindow:window completionHandler:^(NSModalResponse result) {
        if (result != NSModalResponseOK) return;
        NSError *error = nil;
        if (![text writeToURL:panel.URL atomically:YES encoding:NSUTF8StringEncoding error:&error])
            [self alert:[@"导出失败：" stringByAppendingString:error.localizedDescription]];
    }];
}

- (void)exportLog:(id)sender {
    [self saveText:_log.string prefix:@"serial-log" window:_window];
}
- (void)exportMonitor:(id)sender { [self saveText:_monitorLog.string prefix:@"monitor-data" window:_monitorWindow]; }
- (void)appendText:(NSString *)text {
    [_log.textStorage appendAttributedString:[[[NSAttributedString alloc] initWithString:text] autorelease]];
    [_log scrollRangeToVisible:NSMakeRange(_log.string.length, 0)];
}
- (void)alert:(NSString *)message {
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    alert.messageText = @"网络与串口工具"; alert.informativeText = message; [alert runModal];
}
- (BOOL)windowShouldClose:(NSWindow *)sender { [NSApp terminate:nil]; return YES; }
- (void)applicationWillTerminate:(NSNotification *)note { [self stopTimer]; GoDisconnect(); }
@end

void UIAppend(const char *text) {
    NSString *value = [[NSString alloc] initWithUTF8String:text ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{ [(AppDelegate *)NSApp.delegate appendText:value]; });
    [value release];
}

void UIMonitorAppend(const char *text) {
    NSString *value = [[NSString alloc] initWithUTF8String:text ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{ [(AppDelegate *)NSApp.delegate appendMonitorText:value]; });
    [value release];
}

void RunApp(void) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        app.activationPolicy = NSApplicationActivationPolicyRegular;
        AppDelegate *delegate = [[AppDelegate alloc] init];
        app.delegate = delegate;
        [app run];
        [delegate release];
    }
}
