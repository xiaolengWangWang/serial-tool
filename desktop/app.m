#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include <stdlib.h>
#include "app.h"

@interface AppDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate> {
    NSWindow *_window;
    NSWindow *_monitorWindow;
    NSWindow *_vsWindow;
    NSWindow *_toolboxWindow;
    NSTableView *_vsTable;
    NSTableView *_dataTable;
    NSMutableArray *_packets;
    NSMutableArray *_visiblePackets;
    NSPopUpButton *_dirFilter;
    NSComboBox *_vsIP;
    NSTextField *_vsPort;
    NSMutableArray *_vsList;
    NSPopUpButton *_mode, *_bridgeProtocol, *_role, *_history, *_sendHistory, *_favorites;
    NSComboBox *_ports, *_baud, *_data, *_stop, *_parity, *_eol, *_ip;
    NSTextField *_port, *_endpointLabel, *_roleLabel, *_ipLabel, *_portLabel, *_protocolLabel, *_status, *_interval, *_toolboxInput, *_toolboxOutput, *_searchField;
    NSArray *_serialControls;
    NSButton *_refresh, *_connect, *_hexView, *_hexSend, *_timerButton, *_loopButton, *_loopSend;
    NSTextField *_loopCount;
    NSTextView *_send, *_monitorLog, *_sysLog;
    NSTimer *_sendTimer;
    NSPopUpButton *_timeFilter;
    NSTextField *_statsLabel;
    NSTextView *_detailView;
    NSInteger _rxCount, _txCount;
    BOOL _connected;
    BOOL _monitorPaused;
}
- (void)appendText:(NSString *)text;
- (void)appendMonitorText:(NSString *)text;
- (NSString *)sendCurrentData;
- (void)stopTimer;
- (void)addPacketWithTS:(NSString *)ts dir:(NSString *)dir hex:(NSString *)hex ascii:(NSString *)ascii len:(NSInteger)len;
- (void)updatePacketStats;
- (void)loopDone;
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

static void Item(NSMenu *menu, NSString *title, SEL action, NSString *key, NSEventModifierFlags mask) {
    NSMenuItem *item = [menu addItemWithTitle:title action:action keyEquivalent:key];
    item.keyEquivalentModifierMask = mask;
}

static void Submenu(NSMenu *mainMenu, NSString *title, NSMenu *submenu) {
    NSMenuItem *item = [mainMenu addItemWithTitle:title action:NULL keyEquivalent:@""];
    item.submenu = submenu;
}

@implementation AppDelegate
- (void)applicationDidFinishLaunching:(NSNotification *)note {
    [self buildMenu];
    _window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1040, 700)
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
        backing:NSBackingStoreBuffered defer:NO];
    char *ver = GoVersion();
    NSString *version = [NSString stringWithUTF8String:ver ?: ""];
    free(ver);
    _window.title = [NSString stringWithFormat:@"CommBox v%@", version];
    _window.contentMinSize = NSMakeSize(1040, 700);
    _window.delegate = self;
    [_window center];
    NSView *view = _window.contentView;

    // 发送区底色（比窗口背景略深，区分数据区）
    NSView *sendBg = [[[NSView alloc] initWithFrame:NSMakeRect(310, 0, 730, 272)] autorelease];
    sendBg.wantsLayer = YES;
    sendBg.layer.backgroundColor = [[NSColor colorWithWhite:0.94 alpha:1.0] CGColor];
    sendBg.autoresizingMask = NSViewWidthSizable;
    [view addSubview:sendBg];
    // 竖分隔线：左面板 | 右内容
    NSBox *vSep = [[[NSBox alloc] initWithFrame:NSMakeRect(309, 0, 2, 700)] autorelease];
    vSep.boxType = NSBoxSeparator; vSep.autoresizingMask = NSViewHeightSizable;
    [view addSubview:vSep];
    // 横分隔线：数据区 | 发送区
    NSBox *hSep = [[[NSBox alloc] initWithFrame:NSMakeRect(310, 272, 730, 1)] autorelease];
    hSep.boxType = NSBoxSeparator; hSep.autoresizingMask = NSViewWidthSizable;
    [view addSubview:hSep];

    NSBox *config = [[[NSBox alloc] initWithFrame:NSMakeRect(20, 20, 280, 660)] autorelease];
    config.title = @"连接配置";
    config.autoresizingMask = NSViewHeightSizable;
    [view addSubview:config];

    NSTextField *modeLabel = Label(@"工作模式", NSMakeRect(40, 620, 100, 22));
    [view addSubview:modeLabel];
    _mode = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(40, 586, 240, 30) pullsDown:NO];
    [_mode addItemsWithTitles:@[@"串口", @"TCP", @"UDP", @"串口服务器", @"HTTP 客户端"]];
    _mode.target = self; _mode.action = @selector(modeChanged:);
    [view addSubview:_mode];

    _endpointLabel = [Label(@"串口", NSMakeRect(40, 548, 150, 22)) retain];
    [view addSubview:_endpointLabel];
    _ports = [Combo(NSMakeRect(40, 514, 174, 30), @[], @"") retain];
    [view addSubview:_ports];
    _refresh = [[NSButton buttonWithTitle:@"刷新" target:self action:@selector(refresh:)] retain];
    _refresh.frame = NSMakeRect(220, 514, 60, 30); [view addSubview:_refresh];

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

    // 网络控件:协议(仅串口服务器)、角色(服务端/客户端)、IP、端口。位置由 modeChanged 按模式重排。
    _protocolLabel = [Label(@"网络协议", NSMakeRect(40, 310, 100, 22)) retain]; [view addSubview:_protocolLabel];
    _bridgeProtocol = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(40, 278, 110, 30) pullsDown:NO];
    [_bridgeProtocol addItemsWithTitles:@[@"TCP", @"UDP"]]; [view addSubview:_bridgeProtocol];
    _roleLabel = [Label(@"角色", NSMakeRect(165, 310, 100, 22)) retain]; [view addSubview:_roleLabel];
    _role = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(165, 278, 115, 30) pullsDown:NO];
    [_role addItemsWithTitles:@[@"服务端", @"客户端"]];
    _role.target = self; _role.action = @selector(roleChanged:); [view addSubview:_role];
    _ipLabel = [Label(@"IP 地址", NSMakeRect(40, 242, 100, 22)) retain]; [view addSubview:_ipLabel];
    _ip = [Combo(NSMakeRect(40, 208, 150, 30), @[], @"") retain]; // 可下拉选择本机 IP,也可手输
    [view addSubview:_ip];
    _portLabel = [Label(@"端口", NSMakeRect(196, 242, 84, 22)) retain]; [view addSubview:_portLabel];
    _port = [[NSTextField alloc] initWithFrame:NSMakeRect(196, 208, 84, 30)];
    _port.placeholderString = @"9000"; [view addSubview:_port];

    _status = [Label(@"● 未连接", NSMakeRect(30, 150, 260, 52)) retain];
    _status.alignment = NSTextAlignmentCenter;
    _status.textColor = NSColor.secondaryLabelColor;
    _status.usesSingleLineMode = NO;
    _status.maximumNumberOfLines = 3;
    ((NSTextFieldCell *)_status.cell).wraps = YES;
    [view addSubview:_status];
    _connect = [[NSButton buttonWithTitle:@"连接" target:self action:@selector(toggleConnect:)] retain];
    _connect.frame = NSMakeRect(40, 112, 240, 36); _connect.keyEquivalent = @"\r";
    [view addSubview:_connect];
    _history = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(40, 72, 240, 28) pullsDown:YES];
    [_history addItemWithTitle:@"历史连接"];
    _history.target = self; _history.action = @selector(historySelected:);
    [view addSubview:_history];
    NSTextField *addressHint = Label(@"服务端绑定本机地址，客户端填写远程地址", NSMakeRect(40, 34, 240, 22));
    addressHint.font = [NSFont systemFontOfSize:11]; addressHint.textColor = NSColor.tertiaryLabelColor;
    addressHint.alignment = NSTextAlignmentCenter; [view addSubview:addressHint];

    NSTextField *receiveTitle = Label(@"接收数据", NSMakeRect(320, 655, 120, 24));
    receiveTitle.font = [NSFont boldSystemFontOfSize:14];
    receiveTitle.autoresizingMask = NSViewMinYMargin;
    [view addSubview:receiveTitle];
    _statsLabel = [Label(@"", NSMakeRect(444, 655, 140, 22)) retain];
    _statsLabel.font = [NSFont systemFontOfSize:11];
    _statsLabel.textColor = NSColor.secondaryLabelColor;
    _statsLabel.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_statsLabel];
    _hexView = [[NSButton checkboxWithTitle:@"ASCII 列" target:self action:@selector(hexViewChanged:)] retain];
    _hexView.frame = NSMakeRect(590, 653, 90, 26);
    _hexView.autoresizingMask = NSViewMinXMargin | NSViewMinYMargin;
    [view addSubview:_hexView];
    _hexView.state = NSControlStateValueOn;
    GoSetHexView(1);
    NSButton *monitor = [NSButton buttonWithTitle:@"监控窗口" target:self action:@selector(openMonitor:)];
    monitor.frame = NSMakeRect(700, 652, 110, 28);
    monitor.autoresizingMask = NSViewMinXMargin | NSViewMinYMargin;
    [view addSubview:monitor];
    NSButton *export = [NSButton buttonWithTitle:@"导出" target:self action:@selector(exportLog:)];
    export.frame = NSMakeRect(820, 652, 90, 28);
    export.autoresizingMask = NSViewMinXMargin | NSViewMinYMargin;
    [view addSubview:export];
    NSButton *clear = [NSButton buttonWithTitle:@"清空" target:self action:@selector(clear:)];
    clear.frame = NSMakeRect(930, 652, 90, 28);
    clear.autoresizingMask = NSViewMinXMargin | NSViewMinYMargin;
    [view addSubview:clear];

    NSTextField *searchLabel = Label(@"搜索", NSMakeRect(320, 616, 40, 22));
    searchLabel.autoresizingMask = NSViewMinYMargin;
    [view addSubview:searchLabel];
    _searchField = [[NSTextField alloc] initWithFrame:NSMakeRect(360, 612, 220, 26)];
    _searchField.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_searchField];
    _dirFilter = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(588, 610, 80, 28) pullsDown:NO];
    [_dirFilter addItemsWithTitles:@[@"全部", @"RX", @"TX"]];
    _dirFilter.target = self; _dirFilter.action = @selector(applyFilter);
    _dirFilter.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_dirFilter];
    NSButton *filterBtn = [NSButton buttonWithTitle:@"过滤" target:self action:@selector(applyFilter)];
    filterBtn.frame = NSMakeRect(676, 610, 60, 28);
    filterBtn.autoresizingMask = NSViewMinYMargin;
    [view addSubview:filterBtn];
    NSButton *clearFilterBtn = [NSButton buttonWithTitle:@"清除" target:self action:@selector(clearFilter)];
    clearFilterBtn.frame = NSMakeRect(742, 610, 60, 28);
    clearFilterBtn.autoresizingMask = NSViewMinYMargin;
    [view addSubview:clearFilterBtn];
    _timeFilter = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(808, 610, 110, 28) pullsDown:NO];
    [_timeFilter addItemsWithTitles:@[@"全部", @"1分钟", @"5分钟", @"30分钟"]];
    _timeFilter.target = self; _timeFilter.action = @selector(applyFilter);
    _timeFilter.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_timeFilter];

    NSTabView *tabView = [[NSTabView alloc] initWithFrame:NSMakeRect(320, 276, 700, 334)];
    tabView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;

    // 数据 Tab — NSTableView 结构化报文表格
    _packets = [[NSMutableArray alloc] init];
    _visiblePackets = [[NSMutableArray alloc] init];
    // 数据 tab 容器：表格(上，可伸缩) + 详情(下，固定 90px)
    NSView *dataContainer = [[[NSView alloc] initWithFrame:NSMakeRect(0, 0, 700, 334)] autorelease];
    dataContainer.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;

    // 详情区（底部固定 90px）
    NSScrollView *detailScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, 700, 90)] autorelease];
    detailScroll.borderType = NSBezelBorder; detailScroll.hasVerticalScroller = YES;
    detailScroll.autoresizingMask = NSViewWidthSizable;
    _detailView = [[NSTextView alloc] initWithFrame:detailScroll.contentView.bounds];
    _detailView.editable = NO;
    _detailView.font = [NSFont monospacedSystemFontOfSize:11 weight:NSFontWeightRegular];
    _detailView.autoresizingMask = NSViewWidthSizable;
    detailScroll.documentView = _detailView;
    [dataContainer addSubview:detailScroll];

    NSBox *detailSep = [[[NSBox alloc] initWithFrame:NSMakeRect(0, 90, 700, 1)] autorelease];
    detailSep.boxType = NSBoxSeparator; detailSep.autoresizingMask = NSViewWidthSizable;
    [dataContainer addSubview:detailSep];

    // 数据表格（91px 以上，随窗口伸缩）
    NSScrollView *dataScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(0, 91, 700, 243)] autorelease];
    dataScroll.borderType = NSBezelBorder; dataScroll.hasVerticalScroller = YES; dataScroll.hasHorizontalScroller = YES;
    dataScroll.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    _dataTable = [[NSTableView alloc] initWithFrame:dataScroll.contentView.bounds];
    _dataTable.font = [NSFont monospacedSystemFontOfSize:12 weight:NSFontWeightRegular];
    _dataTable.usesAlternatingRowBackgroundColors = YES;
    _dataTable.dataSource = self; _dataTable.delegate = self;
    NSTableColumn *tc0 = [[[NSTableColumn alloc] initWithIdentifier:@"ts"] autorelease];
    tc0.title = @"时间"; tc0.width = 100; [_dataTable addTableColumn:tc0];
    NSTableColumn *tc1 = [[[NSTableColumn alloc] initWithIdentifier:@"dir"] autorelease];
    tc1.title = @"方向"; tc1.width = 45; [_dataTable addTableColumn:tc1];
    NSTableColumn *tc2 = [[[NSTableColumn alloc] initWithIdentifier:@"hex"] autorelease];
    tc2.title = @"HEX"; tc2.width = 300; [_dataTable addTableColumn:tc2];
    NSTableColumn *tc3 = [[[NSTableColumn alloc] initWithIdentifier:@"ascii"] autorelease];
    tc3.title = @"ASCII"; tc3.width = 180; [_dataTable addTableColumn:tc3];
    NSTableColumn *tc4 = [[[NSTableColumn alloc] initWithIdentifier:@"len"] autorelease];
    tc4.title = @"长度"; tc4.width = 65; [_dataTable addTableColumn:tc4];
    dataScroll.documentView = _dataTable;
    NSMenu *tableMenu = [[[NSMenu alloc] initWithTitle:@""] autorelease];
    [tableMenu addItemWithTitle:@"复制 HEX" action:@selector(copyPacketHex:) keyEquivalent:@""];
    [tableMenu addItemWithTitle:@"复制 ASCII" action:@selector(copyPacketASCII:) keyEquivalent:@""];
    [tableMenu addItemWithTitle:@"复制整行" action:@selector(copyPacketAll:) keyEquivalent:@""];
    _dataTable.menu = tableMenu;
    [dataContainer addSubview:dataScroll];

    NSTabViewItem *dataItem = [[[NSTabViewItem alloc] initWithIdentifier:@"data"] autorelease];
    dataItem.label = @"数据"; dataItem.view = dataContainer;
    [tabView addTabViewItem:dataItem];

    NSScrollView *logScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, 700, 290)] autorelease];
    logScroll.borderType = NSBezelBorder; logScroll.hasVerticalScroller = YES;
    logScroll.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    _sysLog = [[NSTextView alloc] initWithFrame:logScroll.contentView.bounds];
    _sysLog.editable = NO; _sysLog.font = [NSFont monospacedSystemFontOfSize:13 weight:NSFontWeightRegular];
    _sysLog.autoresizingMask = NSViewWidthSizable; logScroll.documentView = _sysLog;
    NSTabViewItem *logItem = [[[NSTabViewItem alloc] initWithIdentifier:@"log"] autorelease];
    logItem.label = @"日志"; logItem.view = logScroll;
    [tabView addTabViewItem:logItem];

    [view addSubview:tabView];

    NSTextField *sendTitle = Label(@"发送数据", NSMakeRect(320, 242, 120, 24));
    sendTitle.font = [NSFont boldSystemFontOfSize:14]; [view addSubview:sendTitle];
    _hexSend = [[NSButton checkboxWithTitle:@"HEX 发送" target:nil action:nil] retain];
    _hexSend.state = NSControlStateValueOn;
    _hexSend.frame = NSMakeRect(460, 240, 100, 26); [view addSubview:_hexSend];
    _loopSend = [[NSButton checkboxWithTitle:@"循环" target:nil action:nil] retain];
    _loopSend.frame = NSMakeRect(570, 240, 52, 26); [view addSubview:_loopSend];
    _loopCount = [[NSTextField alloc] initWithFrame:NSMakeRect(626, 238, 52, 28)];
    _loopCount.placeholderString = @"0=∞"; _loopCount.stringValue = @"0";
    [view addSubview:_loopCount];
    [view addSubview:Label(@"行尾", NSMakeRect(688, 242, 36, 24))];
    _eol = [Combo(NSMakeRect(726, 238, 80, 30), @[@"无",@"LF",@"CR",@"CRLF"], @"无") retain];
    [view addSubview:_eol];
    [view addSubview:Label(@"间隔(ms)", NSMakeRect(816, 242, 64, 24))];
    _interval = [[NSTextField alloc] initWithFrame:NSMakeRect(882, 238, 98, 30)];
    _interval.stringValue = @"1000"; _interval.alignment = NSTextAlignmentRight;
    [view addSubview:_interval];

    [view addSubview:Label(@"历史", NSMakeRect(320, 188, 40, 24))];
_sendHistory = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(360, 184, 140, 26) pullsDown:NO];
_sendHistory.target = self; _sendHistory.action = @selector(sendHistorySelected:);
[view addSubview:_sendHistory];
[view addSubview:Label(@"收藏", NSMakeRect(508, 188, 40, 24))];
_favorites = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(548, 184, 140, 26) pullsDown:NO];
_favorites.target = self; _favorites.action = @selector(favoriteSelected:);
[view addSubview:_favorites];
NSButton *favBtn = [NSButton buttonWithTitle:@"收藏当前" target:self action:@selector(saveFavorite:)];
favBtn.frame = NSMakeRect(696, 182, 90, 28); [view addSubview:favBtn];
NSButton *delBtn = [NSButton buttonWithTitle:@"删除" target:self action:@selector(deleteFavorite:)];
delBtn.frame = NSMakeRect(790, 182, 70, 28); [view addSubview:delBtn];

NSScrollView *sendScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(320, 44, 590, 142)] autorelease];
    sendScroll.borderType = NSBezelBorder; sendScroll.hasVerticalScroller = YES; sendScroll.autoresizingMask = NSViewWidthSizable;
    _send = [[NSTextView alloc] initWithFrame:sendScroll.contentView.bounds];
    _send.font = [NSFont monospacedSystemFontOfSize:13 weight:NSFontWeightRegular];
    _send.autoresizingMask = NSViewWidthSizable; sendScroll.documentView = _send; [view addSubview:sendScroll];
    NSButton *sendButton = [NSButton buttonWithTitle:@"发送一次" target:self action:@selector(send:)];
    sendButton.frame = NSMakeRect(925, 176, 95, 54); sendButton.autoresizingMask = NSViewMinXMargin; [view addSubview:sendButton];
    _loopButton = [[NSButton buttonWithTitle:@"循环发送" target:self action:@selector(toggleLoop:)] retain];
    _loopButton.frame = NSMakeRect(925, 116, 95, 54); _loopButton.autoresizingMask = NSViewMinXMargin; [view addSubview:_loopButton];
    _timerButton = [[NSButton buttonWithTitle:@"开始定时" target:self action:@selector(toggleTimer:)] retain];
    _timerButton.frame = NSMakeRect(925, 56, 95, 54); _timerButton.autoresizingMask = NSViewMinXMargin; [view addSubview:_timerButton];
    NSTextField *hint = Label(@"HEX 示例：01 03 00 00 00 02", NSMakeRect(320, 24, 360, 24));
    hint.textColor = NSColor.secondaryLabelColor; [view addSubview:hint];

    [_window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [self modeChanged:nil];
    [self startStatsTimer];
    [self reloadHistory];
    [self refreshSendHistory];
    [self refreshFavorites];
    char *database = GoDatabaseInfo();
    NSString *databaseInfo = [NSString stringWithUTF8String:database ?: ""]; free(database);
    if ([databaseInfo hasPrefix:@"错误:"]) [self alert:databaseInfo];
    else [self appendText:[NSString stringWithFormat:@"[SQLite 数据目录：%@]\n", databaseInfo]];
}

- (void)buildMenu {
    NSMenu *mainMenu = [[[NSMenu alloc] init] autorelease];

    NSMenu *appMenu = [[[NSMenu alloc] init] autorelease];
    Item(appMenu, @"隐藏", @selector(hide:), @"h", NSEventModifierFlagCommand);
    [appMenu addItem:[NSMenuItem separatorItem]];
    Item(appMenu, @"退出", @selector(terminate:), @"q", NSEventModifierFlagCommand);
    Submenu(mainMenu, @"", appMenu);

    NSMenu *actionMenu = [[[NSMenu alloc] initWithTitle:@"操作"] autorelease];
    Item(actionMenu, @"新建实例", @selector(newInstance:), @"n", NSEventModifierFlagCommand);
    Item(actionMenu, @"连接 / 断开", @selector(toggleConnect:), @"l", NSEventModifierFlagCommand);
    Item(actionMenu, @"发送一次", @selector(send:), @"\r", NSEventModifierFlagCommand);
    Item(actionMenu, @"定时发送开关", @selector(toggleTimer:), @"t", NSEventModifierFlagCommand);
    Item(actionMenu, @"刷新串口", @selector(refresh:), @"r", NSEventModifierFlagCommand);
    Item(actionMenu, @"虚拟串口映射", @selector(openVSerialManager:), @"v", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    Item(actionMenu, @"工具箱", @selector(openToolbox:), @"b", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    Submenu(mainMenu, @"操作", actionMenu);

    NSMenu *editMenu = [[[NSMenu alloc] initWithTitle:@"编辑"] autorelease];
    Item(editMenu, @"剪切", @selector(cut:), @"x", NSEventModifierFlagCommand);
    Item(editMenu, @"复制", @selector(copy:), @"c", NSEventModifierFlagCommand);
    Item(editMenu, @"粘贴", @selector(paste:), @"v", NSEventModifierFlagCommand);
    Item(editMenu, @"全选", @selector(selectAll:), @"a", NSEventModifierFlagCommand);
    Submenu(mainMenu, @"编辑", editMenu);

    NSMenu *viewMenu = [[[NSMenu alloc] initWithTitle:@"视图"] autorelease];
    Item(viewMenu, @"清空接收区", @selector(clear:), @"k", NSEventModifierFlagCommand);
    Item(viewMenu, @"导出接收数据", @selector(exportLog:), @"e", NSEventModifierFlagCommand);
    Item(viewMenu, @"ASCII 列开关", @selector(toggleHexView:), @"h", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    Item(viewMenu, @"监控窗口", @selector(openMonitor:), @"m", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    Submenu(mainMenu, @"视图", viewMenu);

    NSApp.mainMenu = mainMenu;
}

- (void)toggleHexView:(id)sender {
    _hexView.state = _hexView.state == NSControlStateValueOn ? NSControlStateValueOff : NSControlStateValueOn;
    GoSetHexView(_hexView.state == NSControlStateValueOn);
}

- (NSString *)modeName { return _mode.titleOfSelectedItem; }
- (BOOL)isSerialMode { return [[self modeName] isEqualToString:@"串口"]; }
- (BOOL)isBridgeMode { return [[self modeName] isEqualToString:@"串口服务器"]; }
- (BOOL)isHTTPMode { return [[self modeName] isEqualToString:@"HTTP 客户端"]; }
- (BOOL)isNetworkMode { NSString *m = [self modeName]; return [m isEqualToString:@"TCP"] || [m isEqualToString:@"UDP"]; }
- (BOOL)isServerMode {
    if ([self isNetworkMode] || [self isBridgeMode]) return [_role.titleOfSelectedItem isEqualToString:@"服务端"];
    return NO;
}

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
    // 把后台运行的虚拟串口设备也列出来,可直接在串口模式打开
    char *vraw = GoListVSerialLinks();
    NSString *vlinks = [NSString stringWithUTF8String:vraw ?: ""]; free(vraw);
    if (vlinks.length) [_ports addItemsWithObjectValues:[vlinks componentsSeparatedByString:@"\n"]];
    if (_ports.numberOfItems) [_ports selectItemAtIndex:0];
}

// 每秒刷新状态栏(连接状态 + RX/TX/运行时间/重连/错误)。
- (void)startStatsTimer {
    [NSTimer scheduledTimerWithTimeInterval:1.0 target:self selector:@selector(refreshStats:) userInfo:nil repeats:YES];
}

- (void)refreshStats:(NSTimer *)timer {
    char *raw = GoStats();
    NSString *text = [NSString stringWithUTF8String:raw ?: ""];
    free(raw);
    if (text.length) _status.stringValue = text;
}

- (void)refreshSendHistory {
    [_sendHistory removeAllItems];
    char *raw = GoRecentSends();
    NSString *v = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    if (v.length) [_sendHistory addItemsWithTitles:[v componentsSeparatedByString:@"\n"]];
}

- (void)refreshFavorites {
    [_favorites removeAllItems];
    char *raw = GoFavoriteNames();
    NSString *v = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    if (v.length) [_favorites addItemsWithTitles:[v componentsSeparatedByString:@"\n"]];
}

- (void)sendHistorySelected:(id)sender {
    NSString *txt = _sendHistory.titleOfSelectedItem;
    if (txt.length) _send.string = txt;
}

- (void)favoriteSelected:(id)sender {
    NSString *name = _favorites.titleOfSelectedItem;
    if (!name.length) return;
    char *raw = GoFavorite((char *)name.UTF8String);
    NSString *v = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    if (v.length) _send.string = v;
}

- (void)saveFavorite:(id)sender {
    NSString *content = _send.string;
    if (!content.length) { [self alert:@"请先输入要收藏的报文"]; return; }
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    alert.messageText = @"收藏当前报文";
    alert.informativeText = @"输入收藏名称:";
    [alert addButtonWithTitle:@"保存"];
    [alert addButtonWithTitle:@"取消"];
    NSTextField *field = [[NSTextField alloc] initWithFrame:NSMakeRect(0, 0, 260, 24)];
    alert.accessoryView = field;
    if ([alert runModal] != NSAlertFirstButtonReturn) return;
    NSString *name = [field.stringValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceCharacterSet]];
    if (!name.length) return;
    char *err = GoSaveFavorite((char *)name.UTF8String, (char *)content.UTF8String);
    NSString *msg = [NSString stringWithUTF8String:err ?: ""]; free(err);
    if (msg.length) { [self alert:msg]; return; }
    [self refreshFavorites];
    [self appendText:[NSString stringWithFormat:@"[已收藏报文:%@]\n", name]];
}

- (void)deleteFavorite:(id)sender {
    NSString *name = _favorites.titleOfSelectedItem;
    if (!name.length) { [self alert:@"请先选择要删除的收藏"]; return; }
    GoDeleteFavorite((char *)name.UTF8String);
    [self refreshFavorites];
    [self appendText:[NSString stringWithFormat:@"[已删除收藏:%@]\n", name]];
}

- (void)openToolbox:(id)sender {
    if (!_toolboxWindow) {
        _toolboxWindow = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 560, 270)
            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
            backing:NSBackingStoreBuffered defer:NO];
        _toolboxWindow.title = @"工具箱";
        _toolboxWindow.releasedWhenClosed = NO;
        NSView *v = _toolboxWindow.contentView;

        [v addSubview:Label(@"输入(HEX 校验用 01 03 00 0A；Base64/Unix 时间戳直接输文本/数字)", NSMakeRect(16, 228, 520, 20))];
        _toolboxInput = [[NSTextField alloc] initWithFrame:NSMakeRect(16, 196, 528, 26)];
        [v addSubview:_toolboxInput];

        NSArray *titles = @[@"CRC16 Modbus", @"CRC16", @"CRC32", @"XOR", @"SUM"];
        for (NSUInteger i = 0; i < titles.count; i++) {
            NSButton *b = [NSButton buttonWithTitle:titles[i] target:self action:@selector(calcChecksum:)];
            b.frame = NSMakeRect(16 + i * 108, 158, 104, 28);
            b.tag = (NSInteger)i;
            [v addSubview:b];
        }
        NSArray *titles2 = @[@"Base64 编码", @"Base64 解码", @"Unix 时间戳"];
        NSArray *kinds2  = @[@"base64enc",   @"base64dec",   @"unixtime"];
        for (NSUInteger i = 0; i < titles2.count; i++) {
            NSButton *b = [NSButton buttonWithTitle:titles2[i] target:self action:@selector(calcToolbox:)];
            b.frame = NSMakeRect(16 + i * 120, 120, 116, 28);
            b.tag = (NSInteger)i;
            [v addSubview:b];
            objc_setAssociatedObject(b, "kind", kinds2[i], OBJC_ASSOCIATION_RETAIN_NONATOMIC);
        }
        _toolboxOutput = [[NSTextField alloc] initWithFrame:NSMakeRect(16, 76, 528, 26)];
        _toolboxOutput.editable = NO; _toolboxOutput.bordered = NO; _toolboxOutput.drawsBackground = NO;
        [v addSubview:_toolboxOutput];
        [_toolboxWindow center];
    }
    [_toolboxWindow makeKeyAndOrderFront:nil];
}

- (void)calcChecksum:(NSButton *)sender {
    NSArray *kinds = @[@"modbus", @"crc16", @"crc32", @"xor", @"sum"];
    NSString *kind = kinds[sender.tag];
    char *raw = GoChecksum((char *)kind.UTF8String, (char *)_toolboxInput.stringValue.UTF8String);
    NSString *result = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    _toolboxOutput.stringValue = result;
}

- (void)calcToolbox:(NSButton *)sender {
    NSString *kind = objc_getAssociatedObject(sender, "kind");
    char *raw = GoChecksum((char *)kind.UTF8String, (char *)_toolboxInput.stringValue.UTF8String);
    NSString *result = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    _toolboxOutput.stringValue = result;
}

- (void)resetToDisconnected {
    [self stopTimer];
    _connected = NO;
    _mode.enabled = YES;
    [self setStatus:@"● 未连接" color:NSColor.secondaryLabelColor];
    [self modeChanged:nil];
}

// 新建实例:用 open -n 强制再启动一个进程(多开)。
- (void)newInstance:(id)sender {
    NSString *path = [[NSBundle mainBundle] bundlePath];
    NSTask *task = [[NSTask alloc] init];
    task.launchPath = @"/usr/bin/open";
    task.arguments = @[@"-n", path];
    [task launch];
}

// 连接被动断开(远端关闭、串口拔出等),由 Go 引擎的 onClosed 回调触发。
- (void)connectionClosed {
    if (!_connected) return;
    GoDisconnect();
    [self resetToDisconnected];
}

- (void)toggleConnect:(id)sender {
    if (_connected) {
        GoDisconnect();
        [self resetToDisconnected];
        [self appendText:@"\n[已停止]\n"];
        return;
    }

    NSString *mode = [self modeName];
    BOOL serial = [self isSerialMode], bridge = [self isBridgeMode];
    BOOL http = [self isHTTPMode], net = [self isNetworkMode], server = [self isServerMode];
    NSString *serialName = _ports.stringValue;
    if ((serial || bridge) && !serialName.length) { [self alert:@"请选择串口"]; return; }

    NSString *endpoint;
    if (http) {
        endpoint = [_ip.stringValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceCharacterSet]];
        if (!endpoint.length) { [self alert:@"请输入 URL"]; return; }
    } else if (net || bridge) {
        NSString *ip = [_ip.stringValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceCharacterSet]];
        NSString *port = [_port.stringValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceCharacterSet]];
        if (port.integerValue <= 0) { [self alert:@"请输入有效端口"]; return; }
        if (!server && !ip.length) { [self alert:@"客户端请输入服务器 IP"]; return; }
        endpoint = [NSString stringWithFormat:@"%@:%@", ip, port]; // 服务端 IP 可空 → ":端口"
    } else {
        endpoint = serialName;
    }

    BOOL hex = _hexView.state == NSControlStateValueOn;
    char *err;
    if (bridge) err = GoStartSerialServer((char *)serialName.UTF8String, _baud.intValue, _data.intValue, _stop.intValue,
                                           (char *)_parity.stringValue.UTF8String,
                                           (char *)_bridgeProtocol.titleOfSelectedItem.UTF8String,
                                           (char *)_role.titleOfSelectedItem.UTF8String,
                                           (char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"TCP"]) err = server ? GoListen((char *)endpoint.UTF8String, hex)
                                                         : GoConnectTCP((char *)endpoint.UTF8String, hex);
    else if ([mode isEqualToString:@"UDP"]) err = server ? GoListenUDP((char *)endpoint.UTF8String, hex)
                                                         : GoConnectUDP((char *)endpoint.UTF8String, hex);
    else if (http) err = GoConnectHTTP((char *)endpoint.UTF8String);
    else err = GoConnect((char *)endpoint.UTF8String, _baud.intValue, _data.intValue, _stop.intValue,
                         (char *)_parity.stringValue.UTF8String, hex);
    NSString *message = [NSString stringWithUTF8String:err ?: ""]; free(err);
    if (message.length) { [self alert:message]; return; }

    _connected = YES; _mode.enabled = NO;
    _ports.enabled = NO; _refresh.enabled = NO; _ip.enabled = NO; _port.enabled = NO;
    _role.enabled = NO; _bridgeProtocol.enabled = NO;
    for (NSControl *control in _serialControls) control.enabled = NO;
    _connect.title = server ? @"停止监听" : @"断开";
    NSString *modeDesc = net ? [NSString stringWithFormat:@"%@ %@", mode, _role.titleOfSelectedItem] : mode;
    NSString *state = server ? @"● 监听中" : @"● 已连接";
    NSString *detail = bridge ? [NSString stringWithFormat:@"%@ ↔ %@", serialName, endpoint]
                              : [NSString stringWithFormat:@"%@ · %@", modeDesc, endpoint];
    NSString *params = (serial || bridge)
        ? [NSString stringWithFormat:@"%@ %@ %@%@ · ", _baud.stringValue, _data.stringValue, _parity.stringValue, _stop.stringValue]
        : @"";
    [self setStatus:[NSString stringWithFormat:@"%@\n%@\n%@开始 %@", state, detail, params, [self nowTime]]
              color:NSColor.systemGreenColor];
    if (bridge)
        [self appendText:[NSString stringWithFormat:@"[串口服务器已启动：%@ ↔ %@ %@ %@]\n", serialName,
                          _bridgeProtocol.titleOfSelectedItem, _role.titleOfSelectedItem, endpoint]];
    else
        [self appendText:[NSString stringWithFormat:@"[%@ %@ %@]\n", server ? @"正在监听" : @"已连接", modeDesc, endpoint]];
    [self reloadHistory];
}

- (NSString *)sendCurrentData {
    BOOL hex = _hexSend.state == NSControlStateValueOn;
    char *err;
    if ([self isHTTPMode])
        err = GoHTTPRequest((char *)_send.string.UTF8String);
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

- (void)toggleLoop:(id)sender {
    BOOL loopEnabled = _loopSend.state == NSControlStateValueOn;
    NSString *input = _send.string;
    BOOL hex = _hexSend.state == NSControlStateValueOn;
    NSString *eol = _eol.stringValue;
    int count = loopEnabled ? (int)_loopCount.integerValue : 1;
    int ms = (int)_interval.integerValue;
    char *raw = GoToggleLoop((char *)input.UTF8String, hex ? 1 : 0, (char *)eol.UTF8String, count, ms);
    NSString *result = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    if ([result isEqualToString:@"started"]) {
        _loopButton.title = @"停止循环";
    } else if ([result isEqualToString:@"stopped"]) {
        _loopButton.title = @"循环发送";
    } else if ([result hasPrefix:@"error:"]) {
        [self alert:[result substringFromIndex:6]];
    }
}

- (void)loopDone { _loopButton.title = @"循环发送"; }

- (void)hexViewChanged:(id)sender {
    NSTableColumn *col = [_dataTable tableColumnWithIdentifier:@"ascii"];
    if (col) col.hidden = (_hexView.state != NSControlStateValueOn);
}
- (void)roleChanged:(id)sender { [self modeChanged:sender]; }
- (void)modeChanged:(id)sender {
    BOOL serial = [self isSerialMode], bridge = [self isBridgeMode];
    BOOL http = [self isHTTPMode], net = [self isNetworkMode];
    BOOL usesSerialName = serial || bridge;
    BOOL usesNet = net || bridge;              // 需要角色 + IP + 端口
    BOOL server = [self isServerMode];

    // 串口名 / URL 标签行
    _endpointLabel.hidden = !(usesSerialName || http);
    _endpointLabel.stringValue = http ? @"请求 URL" : @"串口";
    _ports.hidden = !usesSerialName; _refresh.hidden = !usesSerialName;
    for (NSView *c in _serialControls) c.hidden = !usesSerialName;
    _protocolLabel.hidden = !bridge; _bridgeProtocol.hidden = !bridge;
    _roleLabel.hidden = !usesNet; _role.hidden = !usesNet;
    _ipLabel.hidden = !(usesNet || http); _ip.hidden = !(usesNet || http);
    _ipLabel.stringValue = net ? (server ? @"监听网卡" : @"服务器 IP") : @"IP 地址";
    _portLabel.hidden = !usesNet; _port.hidden = !usesNet;

    // 网络控件按模式重排位置(串口服务器在底部,其余在顶部)
    if (bridge) {
        _roleLabel.frame = NSMakeRect(165, 310, 100, 22); _role.frame = NSMakeRect(165, 278, 115, 30);
        _ipLabel.frame = NSMakeRect(40, 242, 100, 22);     _ip.frame = NSMakeRect(40, 208, 150, 30);
        _portLabel.frame = NSMakeRect(196, 242, 84, 22);   _port.frame = NSMakeRect(196, 208, 84, 30);
    } else if (http) {
        _ipLabel.frame = NSMakeRect(40, 548, 150, 22);     _ip.frame = NSMakeRect(40, 514, 240, 30);
    } else { // TCP / UDP
        _roleLabel.frame = NSMakeRect(40, 548, 100, 22);   _role.frame = NSMakeRect(40, 514, 150, 30);
        _ipLabel.frame = NSMakeRect(40, 466, 100, 22);     _ip.frame = NSMakeRect(40, 432, 150, 30);
        _portLabel.frame = NSMakeRect(196, 466, 84, 22);   _port.frame = NSMakeRect(196, 432, 84, 30);
    }

    _connect.title = bridge ? (server ? @"启动服务器" : @"连接并启动") : (server ? @"开始监听" : @"连接");

    if (usesNet) [self populateIPs:server]; else [_ip removeAllItems]; // 列出本机 IP 供选择
    if (sender) { // 切换模式/角色时填默认值
        if (http) {
            _ip.stringValue = @"http://39.107.191.77:8080/api/data";
        } else if (usesNet) {
            _ip.stringValue = server ? @"0.0.0.0" : [self localIP];
            if (!_port.stringValue.length) _port.stringValue = @"9000";
        }
    }
    if (usesSerialName) [self refresh:nil];

    BOOL en = !_connected;
    _ports.enabled = en; _refresh.enabled = en; _ip.enabled = en; _port.enabled = en;
    _role.enabled = en; _bridgeProtocol.enabled = en;
    for (NSControl *c in _serialControls) c.enabled = en;
}

- (NSString *)localIP {
    char *raw = GoLocalIP();
    NSString *ip = [NSString stringWithUTF8String:raw ?: "127.0.0.1"]; free(raw);
    return ip.length ? ip : @"127.0.0.1";
}

- (void)populateIPs:(BOOL)server {
    char *raw = GoLocalIPs();
    NSString *v = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    NSString *current = _ip.stringValue;
    [_ip removeAllItems];
    if (server) [_ip addItemWithObjectValue:@"0.0.0.0"]; // 服务端:监听所有网卡
    if (v.length) [_ip addItemsWithObjectValues:[v componentsSeparatedByString:@"\n"]];
    _ip.stringValue = current; // 保留用户已输入内容
}

- (NSString *)nowTime {
    NSDateFormatter *fmt = [[[NSDateFormatter alloc] init] autorelease];
    fmt.dateFormat = @"HH:mm:ss";
    return [fmt stringFromDate:[NSDate date]];
}

- (NSDictionary *)parseParameters:(NSString *)params {
    NSMutableDictionary *dict = [NSMutableDictionary dictionary];
    for (NSString *pair in [params componentsSeparatedByString:@","]) {
        NSRange eq = [pair rangeOfString:@"="];
        if (eq.location != NSNotFound)
            dict[[pair substringToIndex:eq.location]] = [pair substringFromIndex:eq.location + 1];
    }
    return dict;
}

- (void)reloadHistory {
    char *raw = GoRecentSessions();
    NSString *value = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    while (_history.numberOfItems > 1) [_history removeItemAtIndex:1];
    for (NSString *line in [value componentsSeparatedByString:@"\n"]) {
        if (!line.length) continue;
        NSArray *f = [line componentsSeparatedByString:@"\x1f"];
        if (f.count < 3) continue;
        NSString *mode = f[0], *endpoint = f[1], *parameters = f[2];
        NSString *where = endpoint.length ? endpoint : ([self parseParameters:parameters][@"serial"] ?: @"");
        NSString *when = (f.count > 3 && [f[3] length] >= 16)
            ? [[f[3] substringWithRange:NSMakeRange(5, 11)] stringByReplacingOccurrencesOfString:@"T" withString:@" "] : @"";
        [_history addItemWithTitle:[NSString stringWithFormat:@"%@ · %@  (%@)", mode, where, when]];
        _history.lastItem.representedObject = @{@"mode": mode, @"endpoint": endpoint, @"parameters": parameters};
    }
}

- (void)fillIPPort:(NSString *)endpoint {
    NSRange colon = [endpoint rangeOfString:@":" options:NSBackwardsSearch];
    if (colon.location != NSNotFound) {
        _ip.stringValue = [endpoint substringToIndex:colon.location];
        _port.stringValue = [endpoint substringFromIndex:colon.location + 1];
    } else {
        _ip.stringValue = endpoint;
    }
}

- (void)historySelected:(id)sender {
    NSDictionary *info = _history.selectedItem.representedObject;
    if (!info) return;
    if (_connected) { [self alert:@"请先断开当前连接再切换历史配置"]; return; }
    NSString *storedMode = info[@"mode"], *endpoint = info[@"endpoint"];
    NSDictionary *p = [self parseParameters:info[@"parameters"]];

    // 存储的是细分模式(如 TCP 客户端),映射到 UI 的"模式 + 角色"
    NSString *uiMode = storedMode, *role = nil;
    if ([storedMode hasPrefix:@"TCP"]) { uiMode = @"TCP"; role = [storedMode hasSuffix:@"服务端"] ? @"服务端" : @"客户端"; }
    else if ([storedMode hasPrefix:@"UDP"]) { uiMode = @"UDP"; role = [storedMode hasSuffix:@"服务端"] ? @"服务端" : @"客户端"; }
    else if ([storedMode isEqualToString:@"串口服务器"]) { role = [p[@"role"] length] ? p[@"role"] : @"服务端"; }

    [_mode selectItemWithTitle:uiMode];
    if (role) [_role selectItemWithTitle:role];
    [self modeChanged:nil];

    if ([self isBridgeMode]) {
        if ([p[@"serial"] length]) _ports.stringValue = p[@"serial"];
        if ([p[@"protocol"] length]) [_bridgeProtocol selectItemWithTitle:p[@"protocol"]];
        [self fillIPPort:endpoint];
    } else if ([self isNetworkMode]) {
        [self fillIPPort:endpoint];
    } else if ([self isHTTPMode]) {
        _ip.stringValue = endpoint;
    } else {
        _ports.stringValue = endpoint;
    }
    if ([p[@"baud"] length]) _baud.stringValue = p[@"baud"];
    if ([p[@"data"] length]) _data.stringValue = p[@"data"];
    if ([p[@"parity"] length]) _parity.stringValue = p[@"parity"];
    if ([p[@"stop"] length]) _stop.stringValue = p[@"stop"];
    [self appendText:[NSString stringWithFormat:@"[已载入历史连接：%@ · %@]\n", storedMode, endpoint.length ? endpoint : (p[@"serial"] ?: @"")]];
}

- (void)openVSerialManager:(id)sender {
    if (!_vsWindow) {
        _vsList = [[NSMutableArray alloc] init];
        _vsWindow = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 620, 420)
            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
            backing:NSBackingStoreBuffered defer:NO];
        _vsWindow.title = @"虚拟串口映射(后台运行,可多个)";
        _vsWindow.releasedWhenClosed = NO;
        NSView *v = _vsWindow.contentView;

        [v addSubview:Label(@"TCP 端点 → 本机虚拟串口。添加后在后台持续运行,断开主连接也不受影响。", NSMakeRect(20, 384, 580, 20))];
        [v addSubview:Label(@"IP", NSMakeRect(20, 350, 24, 22))];
        _vsIP = [Combo(NSMakeRect(46, 346, 200, 28), @[], @"") retain];
        char *raw = GoLocalIPs();
        NSString *ips = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
        if (ips.length) [_vsIP addItemsWithObjectValues:[ips componentsSeparatedByString:@"\n"]];
        [v addSubview:_vsIP];
        [v addSubview:Label(@"端口", NSMakeRect(256, 350, 36, 22))];
        _vsPort = [[NSTextField alloc] initWithFrame:NSMakeRect(296, 346, 80, 28)];
        _vsPort.placeholderString = @"1502"; [v addSubview:_vsPort];
        NSButton *add = [NSButton buttonWithTitle:@"添加映射" target:self action:@selector(addVSerial:)];
        add.frame = NSMakeRect(390, 344, 100, 30); [v addSubview:add];

        NSScrollView *scroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(20, 56, 580, 276)] autorelease];
        scroll.borderType = NSBezelBorder; scroll.hasVerticalScroller = YES;
        scroll.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        _vsTable = [[NSTableView alloc] initWithFrame:scroll.contentView.bounds];
        NSTableColumn *c1 = [[[NSTableColumn alloc] initWithIdentifier:@"addr"] autorelease];
        c1.title = @"TCP 端点"; c1.width = 200; [_vsTable addTableColumn:c1];
        NSTableColumn *c2 = [[[NSTableColumn alloc] initWithIdentifier:@"link"] autorelease];
        c2.title = @"虚拟串口设备"; c2.width = 360; [_vsTable addTableColumn:c2];
        _vsTable.dataSource = self;
        _vsTable.usesAlternatingRowBackgroundColors = YES;
        scroll.documentView = _vsTable; [v addSubview:scroll];

        NSButton *del = [NSButton buttonWithTitle:@"停止选中" target:self action:@selector(removeSelectedVSerial:)];
        del.frame = NSMakeRect(20, 18, 100, 30); [v addSubview:del];
        NSButton *copy = [NSButton buttonWithTitle:@"复制设备路径" target:self action:@selector(copyVSerialPath:)];
        copy.frame = NSMakeRect(128, 18, 130, 30); [v addSubview:copy];
        [v addSubview:Label(@"用 screen /路径 115200 或另一个串口工具打开该设备", NSMakeRect(268, 22, 340, 20))];
        [_vsWindow center];
    }
    [_vsWindow makeKeyAndOrderFront:nil];
}

- (void)addVSerial:(id)sender {
    NSString *ip = [_vsIP.stringValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceCharacterSet]];
    NSString *port = [_vsPort.stringValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceCharacterSet]];
    if (!ip.length || port.integerValue <= 0) { [self alert:@"请输入 IP 和有效端口"]; return; }
    NSString *addr = [NSString stringWithFormat:@"%@:%@", ip, port];
    char *raw = GoAddVSerial((char *)addr.UTF8String);
    NSString *res = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    if ([res hasPrefix:@"错误:"]) { [self alert:res]; return; }
    NSArray *f = [res componentsSeparatedByString:@"\x1f"];
    if (f.count < 3) return;
    [_vsList addObject:@{@"id": f[0], @"addr": f[1], @"link": f[2]}];
    [_vsTable reloadData];
    [self appendText:[NSString stringWithFormat:@"[虚拟串口 #%@ 已创建:%@ → %@]\n", f[0], f[1], f[2]]];
}

- (void)removeSelectedVSerial:(id)sender {
    NSInteger row = _vsTable.selectedRow;
    if (row < 0 || row >= (NSInteger)_vsList.count) { [self alert:@"请先选中一行"]; return; }
    NSDictionary *d = _vsList[row];
    GoRemoveVSerial([d[@"id"] intValue]);
    [_vsList removeObjectAtIndex:row];
    [_vsTable reloadData];
    [self appendText:[NSString stringWithFormat:@"[虚拟串口 #%@ 已停止]\n", d[@"id"]]];
}

- (void)copyVSerialPath:(id)sender {
    NSInteger row = _vsTable.selectedRow;
    if (row < 0 || row >= (NSInteger)_vsList.count) { [self alert:@"请先选中一行"]; return; }
    [[NSPasteboard generalPasteboard] clearContents];
    [[NSPasteboard generalPasteboard] setString:_vsList[row][@"link"] forType:NSPasteboardTypeString];
}

- (NSInteger)numberOfRowsInTableView:(NSTableView *)tableView {
    if (tableView == _vsTable) return (NSInteger)_vsList.count;
    if (tableView == _dataTable) return (NSInteger)_visiblePackets.count;
    return 0;
}
- (id)tableView:(NSTableView *)tableView objectValueForTableColumn:(NSTableColumn *)col row:(NSInteger)row {
    if (tableView == _vsTable) return _vsList[row][col.identifier];
    if (tableView == _dataTable) return _visiblePackets[row][col.identifier];
    return nil;
}
- (void)tableViewSelectionDidChange:(NSNotification *)notification {
    if ([notification object] != _dataTable || !_detailView) return;
    NSInteger row = _dataTable.selectedRow;
    if (row < 0 || row >= (NSInteger)_visiblePackets.count) { _detailView.string = @""; return; }
    NSDictionary *p = _visiblePackets[row];
    NSString *detail = [NSString stringWithFormat:@"[%@] %@  %@\nHEX:   %@\nASCII: %@",
        p[@"ts"] ?: @"", p[@"dir"] ?: @"", p[@"len"] ?: @"",
        p[@"hex"] ?: @"", p[@"ascii"] ?: @""];
    _detailView.string = detail;
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

- (void)appendLogText:(NSString *)text {
    if (!_sysLog) return;
    [_sysLog.textStorage appendAttributedString:[[[NSAttributedString alloc] initWithString:text] autorelease]];
    [_sysLog scrollRangeToVisible:NSMakeRange(_sysLog.string.length, 0)];
}

- (void)clear:(id)sender {
    [_packets removeAllObjects];
    [_visiblePackets removeAllObjects];
    [_dataTable reloadData];
    _rxCount = 0; _txCount = 0;
    [self updatePacketStats];
}
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
    NSMutableString *out = [NSMutableString string];
    for (NSDictionary *p in _visiblePackets) {
        [out appendFormat:@"[%@ %@] %@\n", p[@"ts"], p[@"dir"], p[@"hex"]];
    }
    [self saveText:out prefix:@"serial-log" window:_window];
}
- (void)exportMonitor:(id)sender { [self saveText:_monitorLog.string prefix:@"monitor-data" window:_monitorWindow]; }
- (void)appendText:(NSString *)text {
    // 系统消息统一输出到日志 Tab
    [self appendLogText:text];
}

- (void)applyFilter {
    NSString *kw = [_searchField.stringValue lowercaseString];
    NSString *dir = _dirFilter ? _dirFilter.titleOfSelectedItem : @"全部";
    NSString *timeRange = _timeFilter ? _timeFilter.titleOfSelectedItem : @"全部";
    NSTimeInterval since = 0;
    if ([timeRange isEqualToString:@"1分钟"])       since = [NSDate date].timeIntervalSince1970 - 60;
    else if ([timeRange isEqualToString:@"5分钟"])   since = [NSDate date].timeIntervalSince1970 - 300;
    else if ([timeRange isEqualToString:@"30分钟"])  since = [NSDate date].timeIntervalSince1970 - 1800;
    [_visiblePackets removeAllObjects];
    for (NSDictionary *p in _packets) {
        if (since > 0 && [p[@"epoch"] doubleValue] < since) continue;
        if (![dir isEqualToString:@"全部"] && ![p[@"dir"] isEqualToString:dir]) continue;
        if (kw.length) {
            if (![[p[@"hex"] lowercaseString] containsString:kw] &&
                ![[p[@"ascii"] lowercaseString] containsString:kw]) continue;
        }
        [_visiblePackets addObject:p];
    }
    [_dataTable reloadData];
    if (_visiblePackets.count > 0)
        [_dataTable scrollRowToVisible:(NSInteger)_visiblePackets.count - 1];
}

- (void)clearFilter {
    _searchField.stringValue = @"";
    if (_dirFilter) [_dirFilter selectItemWithTitle:@"全部"];
    if (_timeFilter) [_timeFilter selectItemAtIndex:0];
    [_visiblePackets removeAllObjects];
    [_visiblePackets addObjectsFromArray:_packets];
    [_dataTable reloadData];
    if (_visiblePackets.count > 0)
        [_dataTable scrollRowToVisible:(NSInteger)_visiblePackets.count - 1];
}
- (void)updatePacketStats {
    if (!_statsLabel) return;
    _statsLabel.stringValue = [NSString stringWithFormat:@"RX %ld包  TX %ld包", (long)_rxCount, (long)_txCount];
}

- (void)addPacketWithTS:(NSString *)ts dir:(NSString *)dir hex:(NSString *)hex ascii:(NSString *)ascii len:(NSInteger)len {
    NSDictionary *p = @{
        @"ts": ts, @"dir": dir, @"hex": hex, @"ascii": ascii,
        @"len": [NSString stringWithFormat:@"%ld B", (long)len],
        @"epoch": @([NSDate date].timeIntervalSince1970)
    };
    if ([dir isEqualToString:@"RX"]) _rxCount++;
    else if ([dir isEqualToString:@"TX"]) _txCount++;
    [self updatePacketStats];
    [_packets addObject:p];
    if (_packets.count > 10000) {
        [_packets removeObjectsInRange:NSMakeRange(0, _packets.count - 8000)];
    }
    NSString *kw = [_searchField.stringValue lowercaseString];
    NSString *dirFilt = _dirFilter ? _dirFilter.titleOfSelectedItem : @"全部";
    BOOL passes = YES;
    if (![dirFilt isEqualToString:@"全部"] && ![dir isEqualToString:dirFilt]) passes = NO;
    if (passes && kw.length) {
        if (![[hex lowercaseString] containsString:kw] && ![[ascii lowercaseString] containsString:kw]) passes = NO;
    }
    if (passes) {
        [_visiblePackets addObject:p];
        if (_visiblePackets.count > 10000)
            [_visiblePackets removeObjectsInRange:NSMakeRange(0, _visiblePackets.count - 8000)];
        [_dataTable reloadData];
        [_dataTable scrollRowToVisible:(NSInteger)_visiblePackets.count - 1];
    }
}

- (void)copyPacketHex:(id)sender {
    NSInteger row = _dataTable.clickedRow;
    if (row < 0 || row >= (NSInteger)_visiblePackets.count) return;
    [[NSPasteboard generalPasteboard] clearContents];
    [[NSPasteboard generalPasteboard] setString:_visiblePackets[row][@"hex"] forType:NSPasteboardTypeString];
}

- (void)copyPacketASCII:(id)sender {
    NSInteger row = _dataTable.clickedRow;
    if (row < 0 || row >= (NSInteger)_visiblePackets.count) return;
    [[NSPasteboard generalPasteboard] clearContents];
    [[NSPasteboard generalPasteboard] setString:_visiblePackets[row][@"ascii"] forType:NSPasteboardTypeString];
}

- (void)copyPacketAll:(id)sender {
    NSInteger row = _dataTable.clickedRow;
    if (row < 0 || row >= (NSInteger)_visiblePackets.count) return;
    NSDictionary *p = _visiblePackets[row];
    NSString *line = [NSString stringWithFormat:@"[%@ %@] %@ | %@", p[@"ts"], p[@"dir"], p[@"hex"], p[@"ascii"]];
    [[NSPasteboard generalPasteboard] clearContents];
    [[NSPasteboard generalPasteboard] setString:line forType:NSPasteboardTypeString];
}

- (void)alert:(NSString *)message {
    NSAlert *alert = [[[NSAlert alloc] init] autorelease];
    alert.messageText = @"CommBox"; alert.informativeText = message; [alert runModal];
}
- (BOOL)windowShouldClose:(NSWindow *)sender {
    [sender orderOut:nil];  // 关闭窗口仅隐藏,后台运行(连接/虚拟串口保持)
    return NO;
}
- (BOOL)applicationShouldHandleReopen:(NSApplication *)sender hasVisibleWindows:(BOOL)flag {
    [_window makeKeyAndOrderFront:nil];
    return YES;
}
- (void)applicationWillTerminate:(NSNotification *)note { [self stopTimer]; GoDisconnect(); }
@end

void UIAppend(const char *text) {
    NSString *value = [[NSString alloc] initWithUTF8String:text ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{ [(AppDelegate *)NSApp.delegate appendLogText:value]; });
    [value release];
}

void UIAddPacket(const char *ts, const char *dir, const char *hex, const char *ascii, int len) {
    NSString *nts    = [[NSString alloc] initWithUTF8String:ts    ?: ""];
    NSString *ndir   = [[NSString alloc] initWithUTF8String:dir   ?: ""];
    NSString *nhex   = [[NSString alloc] initWithUTF8String:hex   ?: ""];
    NSString *nascii = [[NSString alloc] initWithUTF8String:ascii ?: ""];
    NSInteger nlen = len;
    dispatch_async(dispatch_get_main_queue(), ^{
        [(AppDelegate *)NSApp.delegate addPacketWithTS:nts dir:ndir hex:nhex ascii:nascii len:nlen];
        [nts release]; [ndir release]; [nhex release]; [nascii release];
    });
}

void UIAppendLog(const char *text) {
    NSString *value = [[NSString alloc] initWithUTF8String:text ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{ [(AppDelegate *)NSApp.delegate appendLogText:value]; });
    [value release];
}

void UIMonitorAppend(const char *text) {
    NSString *value = [[NSString alloc] initWithUTF8String:text ?: ""];
    dispatch_async(dispatch_get_main_queue(), ^{ [(AppDelegate *)NSApp.delegate appendMonitorText:value]; });
    [value release];
}

void UIConnectionClosed(void) {
    dispatch_async(dispatch_get_main_queue(), ^{ [(AppDelegate *)NSApp.delegate connectionClosed]; });
}

void UILoopDone(void) {
    dispatch_async(dispatch_get_main_queue(), ^{ [(AppDelegate *)NSApp.delegate loopDone]; });
}

void RunApp(void) {
    @autoreleasepool {
        NSApplication *app = [NSApplication sharedApplication];
        app.activationPolicy = NSApplicationActivationPolicyRegular;
        NSString *icnsPath = [[NSBundle mainBundle] pathForResource:@"CommBox" ofType:@"icns"];
        if (icnsPath) {
            NSImage *icon = [[[NSImage alloc] initWithContentsOfFile:icnsPath] autorelease];
            if (icon) { [app setApplicationIconImage:icon]; }
        }
        AppDelegate *delegate = [[AppDelegate alloc] init];
        app.delegate = delegate;
        [app run];
        [delegate release];
    }
}
