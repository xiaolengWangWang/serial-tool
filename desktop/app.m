#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include "app.h"

@interface AppDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate> {
    NSWindow *_window;
    NSPopUpButton *_mode;
    NSComboBox *_ports, *_baud, *_data, *_stop, *_parity, *_eol;
    NSTextField *_address, *_endpointLabel, *_status;
    NSArray *_serialControls;
    NSButton *_refresh, *_connect, *_hexView, *_hexSend;
    NSTextView *_log, *_send;
    BOOL _connected;
}
- (void)appendText:(NSString *)text;
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
    [_mode addItemsWithTitles:@[@"串口", @"TCP 服务端", @"TCP 客户端", @"UDP 服务端", @"UDP 客户端"]];
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

    _status = [Label(@"● 未连接", NSMakeRect(40, 112, 240, 24)) retain];
    _status.alignment = NSTextAlignmentCenter;
    _status.textColor = NSColor.secondaryLabelColor;
    [view addSubview:_status];
    _connect = [[NSButton buttonWithTitle:@"连接" target:self action:@selector(toggleConnect:)] retain];
    _connect.frame = NSMakeRect(40, 68, 240, 36); _connect.keyEquivalent = @"\r";
    [view addSubview:_connect];
    NSTextField *addressHint = Label(@"服务端绑定本机地址，客户端填写远程地址", NSMakeRect(40, 34, 240, 22));
    addressHint.font = [NSFont systemFontOfSize:11]; addressHint.textColor = NSColor.tertiaryLabelColor;
    addressHint.alignment = NSTextAlignmentCenter; [view addSubview:addressHint];

    NSTextField *receiveTitle = Label(@"接收数据", NSMakeRect(320, 655, 120, 24));
    receiveTitle.font = [NSFont boldSystemFontOfSize:14]; [view addSubview:receiveTitle];
    _hexView = [[NSButton checkboxWithTitle:@"HEX 显示" target:self action:@selector(hexViewChanged:)] retain];
    _hexView.frame = NSMakeRect(810, 653, 100, 26); _hexView.autoresizingMask = NSViewMinXMargin; [view addSubview:_hexView];
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
    _hexSend.frame = NSMakeRect(740, 240, 100, 26); _hexSend.autoresizingMask = NSViewMinXMargin; [view addSubview:_hexSend];
    [view addSubview:Label(@"行尾", NSMakeRect(850, 242, 36, 24))];
    _eol = [Combo(NSMakeRect(890, 238, 130, 30), @[@"无",@"LF",@"CR",@"CRLF"], @"无") retain];
    _eol.autoresizingMask = NSViewMinXMargin; [view addSubview:_eol];

    NSScrollView *sendScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(320, 58, 590, 172)] autorelease];
    sendScroll.borderType = NSBezelBorder; sendScroll.hasVerticalScroller = YES; sendScroll.autoresizingMask = NSViewWidthSizable;
    _send = [[NSTextView alloc] initWithFrame:sendScroll.contentView.bounds];
    _send.font = [NSFont monospacedSystemFontOfSize:13 weight:NSFontWeightRegular];
    _send.autoresizingMask = NSViewWidthSizable; sendScroll.documentView = _send; [view addSubview:sendScroll];
    NSButton *sendButton = [NSButton buttonWithTitle:@"发送" target:self action:@selector(send:)];
    sendButton.frame = NSMakeRect(925, 58, 95, 172); sendButton.autoresizingMask = NSViewMinXMargin; [view addSubview:sendButton];
    NSTextField *hint = Label(@"HEX 示例：01 03 00 00 00 02", NSMakeRect(320, 24, 360, 24));
    hint.textColor = NSColor.secondaryLabelColor; [view addSubview:hint];

    [_window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [self refresh:nil];
}

- (NSString *)modeName { return _mode.titleOfSelectedItem; }
- (BOOL)isServerMode { return [[self modeName] hasSuffix:@"服务端"]; }
- (BOOL)isNetworkMode { return ![[self modeName] isEqualToString:@"串口"]; }

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
        GoDisconnect(); _connected = NO; _mode.enabled = YES;
        [self setStatus:@"● 未连接" color:NSColor.secondaryLabelColor];
        [self modeChanged:nil]; [self appendText:@"\n[已停止]\n"]; return;
    }

    NSString *mode = [self modeName];
    BOOL network = [self isNetworkMode], server = [self isServerMode];
    NSString *endpoint = network ? _address.stringValue : _ports.stringValue;
    if (!endpoint.length) { [self alert:network ? @"请输入 IP:端口" : @"请选择串口"]; return; }
    if (network && ![endpoint containsString:@":"]) {
        NSInteger port = endpoint.integerValue;
        if (port <= 0) { [self alert:@"地址格式应为 IP:端口，例如 192.168.1.10:9000"]; return; }
        endpoint = server ? [NSString stringWithFormat:@":%ld", (long)port]
                          : [NSString stringWithFormat:@"127.0.0.1:%ld", (long)port];
        _address.stringValue = endpoint;
    }

    BOOL hex = _hexView.state == NSControlStateValueOn;
    char *err;
    if ([mode isEqualToString:@"TCP 服务端"]) err = GoListen((char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"TCP 客户端"]) err = GoConnectTCP((char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"UDP 服务端"]) err = GoListenUDP((char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"UDP 客户端"]) err = GoConnectUDP((char *)endpoint.UTF8String, hex);
    else err = GoConnect((char *)endpoint.UTF8String, _baud.intValue, _data.intValue, _stop.intValue,
                         (char *)_parity.stringValue.UTF8String, hex);
    NSString *message = [NSString stringWithUTF8String:err ?: ""]; free(err);
    if (message.length) { [self alert:message]; return; }

    _connected = YES; _mode.enabled = NO; _ports.enabled = NO; _address.enabled = NO;
    _refresh.enabled = NO; for (NSControl *control in _serialControls) control.enabled = NO;
    _connect.title = server ? @"停止监听" : @"断开";
    [self setStatus:server ? @"● 监听中" : @"● 已连接" color:NSColor.systemGreenColor];
    [self appendText:server
        ? [NSString stringWithFormat:@"[正在监听 %@ %@]\n", mode, endpoint]
        : [NSString stringWithFormat:@"[已连接 %@ %@]\n", mode, endpoint]];
}

- (void)send:(id)sender {
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
    if (message.length) [self alert:message];
}

- (void)hexViewChanged:(id)sender { GoSetHexView(_hexView.state == NSControlStateValueOn); }
- (void)modeChanged:(id)sender {
    BOOL network = [self isNetworkMode], server = [self isServerMode];
    _ports.hidden = network; _refresh.hidden = network; _address.hidden = !network;
    for (NSView *control in _serialControls) control.hidden = network;
    _endpointLabel.stringValue = network ? (server ? @"本机监听地址" : @"远程服务器地址") : @"串口";
    _connect.title = server ? @"开始监听" : @"连接";
    if (sender && network) _address.stringValue = server ? @":9000" : @"127.0.0.1:9000";
    if (!network) [self refresh:nil];
    _ports.enabled = !_connected; _address.enabled = !_connected;
    _refresh.enabled = !_connected; for (NSControl *control in _serialControls) control.enabled = !_connected;
}

- (void)clear:(id)sender { [_log setString:@""]; }
- (void)appendText:(NSString *)text {
    [_log.textStorage appendAttributedString:[[[NSAttributedString alloc] initWithString:text] autorelease]];
    [_log scrollRangeToVisible:NSMakeRange(_log.string.length, 0)];
}
- (void)alert:(NSString *)message {
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    alert.messageText = @"网络与串口工具"; alert.informativeText = message; [alert runModal];
}
- (BOOL)windowShouldClose:(NSWindow *)sender { [NSApp terminate:nil]; return YES; }
- (void)applicationWillTerminate:(NSNotification *)note { GoDisconnect(); }
@end

void UIAppend(const char *text) {
    NSString *value = [[NSString alloc] initWithUTF8String:text ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{ [(AppDelegate *)NSApp.delegate appendText:value]; });
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
