#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include <stdlib.h>
#include <string.h>
#include "app.h"
int RunLayoutChecks(id delegate, NSString *directory);

@interface AppDelegate : NSObject <NSApplicationDelegate, NSWindowDelegate, NSTableViewDataSource, NSTableViewDelegate, NSToolbarDelegate, NSTextFieldDelegate> {
    NSWindow *_window;
    NSWindow *_monitorWindow;
    NSWindow *_vsWindow;
    NSWindow *_toolboxWindow;
    NSWindow *_aiSettingsWindow;
    NSTableView *_vsTable;
    NSTableView *_dataTable;
    NSMutableArray *_packets;
    NSMutableArray *_visiblePackets;
    NSPopUpButton *_dirFilter;
    NSPopUpButton *_typeFilter, *_lengthFilter;
    NSComboBox *_vsIP;
    NSTextField *_vsPort;
    NSMutableArray *_vsList;
    NSPopUpButton *_mode, *_bridgeProtocol, *_role, *_history, *_sendHistory, *_favorites;
    NSComboBox *_ports, *_baud, *_data, *_stop, *_parity, *_eol, *_ip;
    NSTextField *_port, *_endpointLabel, *_roleLabel, *_ipLabel, *_portLabel, *_protocolLabel, *_status, *_interval, *_toolboxInput, *_toolboxOutput, *_searchField;
    NSArray *_serialControls;
    NSButton *_refresh, *_connect, *_hexView, *_hexSend, *_timerButton, *_quickTimerButton, *_loopButton, *_loopSend;
    NSTextField *_loopCount;
    NSTextView *_send, *_monitorLog, *_sysLog, *_analysisResult;
    NSTimer *_sendTimer;
    NSPopUpButton *_timeFilter;
    NSTextField *_statsLabel, *_selectionLabel;
    NSTextField *_analysisStats, *_analysisAIStatus;
    NSPopUpButton *_analysisScope;
    NSTextView *_detailView;
    NSView *_sendBackground, *_analysisSidebar;
    NSBox *_horizontalSeparator;
    NSTabView *_dataView, *_sendView;
    NSView *_leftPane, *_centerPane, *_rightPane;
    NSScrollView *_connectionScroll;
    NSStackView *_connectionCards;
    NSLayoutConstraint *_connectionWidth;
    NSTextField *_connectionDetail, *_connectionMetrics, *_peerInfo;
    BOOL _analysisVisible;
    NSStackView *_filterRow;
    NSView *_endpointForm, *_serialForm, *_networkForm, *_addressForm, *_protocolField, *_portField;
    BOOL _databaseBusy;
    NSInteger _rxCount, _txCount;
    BOOL _connected;
    BOOL _monitorPaused;
    BOOL _aiEnabled;
    NSTextField *_aiBaseURL, *_aiModel, *_aiKey;
    NSButton *_aiEnabledButton;
    NSTextField *_aiKeyHint;
}
- (void)appendText:(NSString *)text;
- (void)appendMonitorText:(NSString *)text;
- (NSString *)sendCurrentData;
- (void)stopTimer;
- (void)addPacketWithTS:(NSString *)ts dir:(NSString *)dir hex:(NSString *)hex ascii:(NSString *)ascii kind:(NSString *)kind len:(NSInteger)len;
- (void)updatePacketStats;
- (void)loopDone;
@end

// Draw with semantic AppKit colors so cards also follow dark appearance.
@interface WorkspaceView : NSView
@end
@implementation WorkspaceView
- (void)drawRect:(NSRect)rect { [NSColor.windowBackgroundColor setFill]; NSRectFill(self.bounds); }
@end

@interface ConnectionCard : NSView
@end
@implementation ConnectionCard
- (void)drawRect:(NSRect)rect {
    NSBezierPath *path = [NSBezierPath bezierPathWithRoundedRect:NSInsetRect(self.bounds, 0.5, 0.5) xRadius:10 yRadius:10];
    [[NSColor.controlBackgroundColor blendedColorWithFraction:0.025 ofColor:NSColor.systemBlueColor] setFill]; [path fill];
    [[NSColor.separatorColor colorWithAlphaComponent:0.3] setStroke]; [path stroke];
}
@end

// A native table keeps file selection compact even with many rotated captures.
@interface DatabaseFilePicker : NSScrollView <NSTableViewDataSource> {
    NSArray *_filenames;
    NSTableView *_table;
}
- (id)initWithFilenames:(NSArray *)filenames;
- (NSArray *)selectedFilenames;
@end

@implementation DatabaseFilePicker
- (NSInteger)tag { return 101; }
- (id)initWithFilenames:(NSArray *)filenames {
    self = [super initWithFrame:NSMakeRect(0, 0, 420, 104)];
    if (self) {
        _filenames = [filenames copy];
        self.borderType = NSBezelBorder;
        self.hasVerticalScroller = YES;
        _table = [[NSTableView alloc] initWithFrame:self.contentView.bounds];
        _table.allowsMultipleSelection = YES;
        _table.allowsEmptySelection = YES;
        _table.usesAlternatingRowBackgroundColors = YES;
        _table.rowHeight = 22;
        _table.headerView = nil;
        _table.columnAutoresizingStyle = NSTableViewLastColumnOnlyAutoresizingStyle;
        NSTableColumn *column = [[[NSTableColumn alloc] initWithIdentifier:@"filename"] autorelease];
        column.width = 400;
        [_table addTableColumn:column];
        _table.dataSource = self;
        [_table setAccessibilityLabel:@"数据库文件，支持多选"];
        self.documentView = _table;
        if (filenames.count) [_table selectRowIndexes:[NSIndexSet indexSetWithIndex:filenames.count - 1] byExtendingSelection:NO];
    }
    return self;
}
- (NSInteger)numberOfRowsInTableView:(NSTableView *)table { return _filenames.count; }
- (id)tableView:(NSTableView *)table objectValueForTableColumn:(NSTableColumn *)column row:(NSInteger)row { return _filenames[row]; }
- (NSArray *)selectedFilenames {
    return [_filenames objectsAtIndexes:_table.selectedRowIndexes];
}
- (void)dealloc { _table.dataSource = nil; [_table release]; [_filenames release]; [super dealloc]; }
@end

static NSTextField *Label(NSString *text, NSRect frame) {
    NSTextField *label = [[[NSTextField alloc] initWithFrame:frame] autorelease];
    label.stringValue = text;
    label.editable = NO;
    label.selectable = NO;
    label.bordered = NO;
    label.drawsBackground = NO;
    label.usesSingleLineMode = YES;
    label.maximumNumberOfLines = 1;
    label.lineBreakMode = NSLineBreakByTruncatingTail;
    return label;
}

static NSComboBox *Combo(NSRect frame, NSArray *items, NSString *value) {
    NSComboBox *box = [[[NSComboBox alloc] initWithFrame:frame] autorelease];
    [box addItemsWithObjectValues:items];
    box.stringValue = value;
    box.numberOfVisibleItems = 10;
    return box;
}

static NSStackView *Row(NSArray<NSView *> *views) {
    NSStackView *row = [NSStackView stackViewWithViews:views];
    row.orientation = NSUserInterfaceLayoutOrientationHorizontal;
    row.alignment = NSLayoutAttributeCenterY;
    row.spacing = 8;
    row.detachesHiddenViews = YES;
    for (NSView *view in views) {
        view.translatesAutoresizingMaskIntoConstraints = NO;
        [view setContentCompressionResistancePriority:NSLayoutPriorityRequired forOrientation:NSLayoutConstraintOrientationHorizontal];
    }
    return row;
}

static void PinRow(NSView *row, NSView *parent, CGFloat top) {
    row.translatesAutoresizingMaskIntoConstraints = NO;
    [parent addSubview:row];
    [NSLayoutConstraint activateConstraints:@[
        [row.leadingAnchor constraintEqualToAnchor:parent.leadingAnchor constant:8],
        [row.trailingAnchor constraintLessThanOrEqualToAnchor:parent.trailingAnchor constant:-8],
        [row.topAnchor constraintEqualToAnchor:parent.topAnchor constant:top],
        [row.heightAnchor constraintEqualToConstant:30]
    ]];
}

static NSStackView *Column(NSArray<NSView *> *views, CGFloat spacing) {
    NSStackView *column = [NSStackView stackViewWithViews:views];
    column.orientation = NSUserInterfaceLayoutOrientationVertical;
    column.alignment = NSLayoutAttributeLeading;
    column.spacing = spacing;
    column.detachesHiddenViews = YES;
    for (NSView *view in views) {
        view.translatesAutoresizingMaskIntoConstraints = NO;
        [view.widthAnchor constraintEqualToAnchor:column.widthAnchor].active = YES;
    }
    return column;
}

static NSStackView *Field(NSTextField *label, NSView *input) {
    label.font = [NSFont systemFontOfSize:11 weight:NSFontWeightMedium];
    label.textColor = NSColor.secondaryLabelColor;
    return Column(@[label, input], 4);
}

static NSView *Card(NSString *title, NSArray<NSView *> *items) {
    NSTextField *heading = Label(title, NSZeroRect);
    heading.font = [NSFont systemFontOfSize:14 weight:NSFontWeightSemibold];
    heading.textColor = NSColor.systemBlueColor;
    NSStackView *content = Column([@[heading] arrayByAddingObjectsFromArray:items], 10);
    NSView *card = [[[ConnectionCard alloc] initWithFrame:NSZeroRect] autorelease];
    content.translatesAutoresizingMaskIntoConstraints = NO;
    [card addSubview:content];
    [NSLayoutConstraint activateConstraints:@[
        [content.leadingAnchor constraintEqualToAnchor:card.leadingAnchor constant:14],
        [content.trailingAnchor constraintEqualToAnchor:card.trailingAnchor constant:-14],
        [content.topAnchor constraintEqualToAnchor:card.topAnchor constant:14],
        [content.bottomAnchor constraintEqualToAnchor:card.bottomAnchor constant:-14]
    ]];
    return card;
}

static NSTextField *StatusLines(NSInteger lines) {
    NSTextField *label = Label(@"", NSZeroRect);
    label.font = [NSFont systemFontOfSize:12];
    label.usesSingleLineMode = NO;
    label.maximumNumberOfLines = lines;
    label.lineBreakMode = NSLineBreakByTruncatingTail;
    [label.heightAnchor constraintEqualToConstant:lines * 18].active = YES;
    return label;
}

static NSStackView *FormPair(NSView *first, NSView *second) {
    NSStackView *row = Row(@[first, second]);
    row.alignment = NSLayoutAttributeTop;
    row.distribution = NSStackViewDistributionFillEqually;
    row.spacing = 12;
    return row;
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
    _window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1280, 700)
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable | NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
        backing:NSBackingStoreBuffered defer:NO];
    char *ver = GoVersion();
    NSString *version = [NSString stringWithUTF8String:ver ?: ""];
    free(ver);
    _window.title = [NSString stringWithFormat:@"CommBox v%@", version];
    _window.contentMinSize = NSMakeSize(1040, 700);
    _window.titleVisibility = NSWindowTitleVisible;
    _window.titlebarAppearsTransparent = NO;
    _window.toolbarStyle = NSWindowToolbarStyleUnifiedCompact;
    NSToolbar *toolbar = [[[NSToolbar alloc] initWithIdentifier:@"CommBoxToolbar"] autorelease];
    toolbar.delegate = self;
    toolbar.displayMode = NSToolbarDisplayModeIconAndLabel;
    toolbar.allowsUserCustomization = NO;
    _window.toolbar = toolbar;
    _window.delegate = self;
    [_window center];
    // Build against one stable content coordinate system; attaching the toolbar
    // must not resize only part of the controls during construction.
    NSView *view = [[[WorkspaceView alloc] initWithFrame:NSMakeRect(0, 0, 1280, 700)] autorelease];
    view.wantsLayer = YES;

    // 发送区底色（比窗口背景略深，区分数据区）
    NSView *sendBg = [[[ConnectionCard alloc] initWithFrame:NSMakeRect(310, 0, 710, 272)] autorelease];
    _sendBackground = sendBg;
    sendBg.autoresizingMask = NSViewMinXMargin;
    [view addSubview:sendBg];
    // 竖分隔线：左面板 | 右内容
    NSBox *vSep = [[[NSBox alloc] initWithFrame:NSMakeRect(309, 0, 2, 700)] autorelease];
    vSep.boxType = NSBoxSeparator; vSep.autoresizingMask = NSViewHeightSizable;
    [view addSubview:vSep];
    // 横分隔线：数据区 | 发送区
    NSBox *hSep = [[[NSBox alloc] initWithFrame:NSMakeRect(310, 272, 710, 1)] autorelease];
    _horizontalSeparator = hSep;
    hSep.boxType = NSBoxSeparator; hSep.autoresizingMask = NSViewMinXMargin;
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
    for (NSView *control in @[modeLabel, _mode, _endpointLabel, _ports, _refresh, baudLabel, dataLabel, _baud, _data,
                              parityLabel, stopLabel, _parity, _stop, _protocolLabel, _bridgeProtocol, _roleLabel, _role,
                              _ipLabel, _ip, _portLabel, _port, _status, _connect, _history, addressHint]) {
        control.autoresizingMask = NSViewMinYMargin | NSViewMaxXMargin;
    }

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
    _searchField = [[NSSearchField alloc] initWithFrame:NSMakeRect(360, 612, 190, 26)];
    _searchField.placeholderString = @"搜索 HEX / ASCII";
    _searchField.delegate = self;
    _searchField.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_searchField];
    _dirFilter = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(558, 610, 70, 28) pullsDown:NO];
    [_dirFilter addItemsWithTitles:@[@"全部方向", @"RX", @"TX"]];
    _dirFilter.target = self; _dirFilter.action = @selector(applyFilter);
    _dirFilter.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_dirFilter];
    _typeFilter = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(634, 610, 70, 28) pullsDown:NO];
    [_typeFilter addItemsWithTitles:@[@"全部类型", @"ASCII", @"HEX"]];
    _typeFilter.target = self; _typeFilter.action = @selector(applyFilter);
    _typeFilter.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_typeFilter];
    _lengthFilter = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(710, 610, 70, 28) pullsDown:NO];
    [_lengthFilter addItemsWithTitles:@[@"全部长度", @"1–8 B", @"9–64 B", @"65 B 以上"]];
    _lengthFilter.target = self; _lengthFilter.action = @selector(applyFilter);
    _lengthFilter.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_lengthFilter];
    NSButton *filterBtn = [NSButton buttonWithTitle:@"过滤" target:self action:@selector(applyFilter)];
    filterBtn.frame = NSMakeRect(892, 610, 56, 28);
    filterBtn.autoresizingMask = NSViewMinYMargin;
    [view addSubview:filterBtn];
    NSButton *clearFilterBtn = [NSButton buttonWithTitle:@"清除" target:self action:@selector(clearFilter)];
    clearFilterBtn.frame = NSMakeRect(954, 610, 60, 28);
    clearFilterBtn.autoresizingMask = NSViewMinYMargin;
    [view addSubview:clearFilterBtn];
    _timeFilter = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(786, 610, 100, 28) pullsDown:NO];
    [_timeFilter addItemsWithTitles:@[@"全部时间", @"近 1 分钟", @"近 5 分钟", @"近 30 分钟"]];
    _timeFilter.target = self; _timeFilter.action = @selector(applyFilter);
    _timeFilter.autoresizingMask = NSViewMinYMargin;
    [view addSubview:_timeFilter];
    [searchLabel removeFromSuperview];
    [filterBtn removeFromSuperview];
    clearFilterBtn.title = @"重置";
    _filterRow = Row(@[_searchField, _dirFilter, _typeFilter, _lengthFilter, _timeFilter, clearFilterBtn]);
    [_searchField.widthAnchor constraintEqualToConstant:160].active = YES;
    for (NSPopUpButton *filter in @[_dirFilter, _typeFilter, _lengthFilter, _timeFilter]) {
        [filter.widthAnchor constraintEqualToConstant:96].active = YES;
    }
    _filterRow.frame = NSMakeRect(320, 610, 700, 30);
    [view addSubview:_filterRow];

    NSTabView *tabView = [[NSTabView alloc] initWithFrame:NSMakeRect(320, 276, 700, 334)];
    _dataView = tabView;
    tabView.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;

    // 数据 Tab — NSTableView 结构化报文表格
    _packets = [[NSMutableArray alloc] init];
    _visiblePackets = [[NSMutableArray alloc] init];
    // 数据 tab 容器：表格(上，可伸缩) + 详情(下，固定 90px)
    NSView *dataContainer = [[[NSView alloc] initWithFrame:NSMakeRect(0, 0, 700, 334)] autorelease];
    dataContainer.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;

    // 详情区（底部固定 90px）
    NSScrollView *detailScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(0, 0, 360, 90)] autorelease];
    detailScroll.borderType = NSBezelBorder; detailScroll.hasVerticalScroller = YES;
    detailScroll.autoresizingMask = NSViewWidthSizable;
    _detailView = [[NSTextView alloc] initWithFrame:detailScroll.contentView.bounds];
    _detailView.editable = NO;
    _detailView.font = [NSFont monospacedSystemFontOfSize:11 weight:NSFontWeightRegular];
    _detailView.autoresizingMask = NSViewWidthSizable;
    detailScroll.documentView = _detailView;
    [dataContainer addSubview:detailScroll];
    NSButton *selectAll = [NSButton buttonWithTitle:@"全选" target:self action:@selector(selectAllPackets:)];
    selectAll.frame = NSMakeRect(370, 50, 58, 28); selectAll.autoresizingMask = NSViewMinXMargin; [dataContainer addSubview:selectAll];
    NSButton *clearSelection = [NSButton buttonWithTitle:@"取消选择" target:self action:@selector(clearPacketSelection:)];
    clearSelection.frame = NSMakeRect(434, 50, 78, 28); clearSelection.autoresizingMask = NSViewMinXMargin; [dataContainer addSubview:clearSelection];
    NSButton *invertSelection = [NSButton buttonWithTitle:@"反选" target:self action:@selector(invertPacketSelection:)];
    invertSelection.frame = NSMakeRect(518, 50, 58, 28); invertSelection.autoresizingMask = NSViewMinXMargin; [dataContainer addSubview:invertSelection];
    NSButton *analyzeSelected = [NSButton buttonWithTitle:@"分析选中数据" target:self action:@selector(analyzeSelected:)];
    analyzeSelected.frame = NSMakeRect(544, 50, 72, 28); analyzeSelected.autoresizingMask = NSViewMinXMargin; [dataContainer addSubview:analyzeSelected];
    NSButton *aiAnalyze = [NSButton buttonWithTitle:@"分析中心" target:self action:@selector(openAnalysisCenter:)];
    aiAnalyze.frame = NSMakeRect(620, 50, 70, 28); aiAnalyze.autoresizingMask = NSViewMinXMargin; [dataContainer addSubview:aiAnalyze];
    _selectionLabel = [[NSTextField alloc] initWithFrame:NSMakeRect(370, 15, 320, 24)];
    _selectionLabel.editable = NO; _selectionLabel.bordered = NO; _selectionLabel.drawsBackground = NO;
    _selectionLabel.textColor = NSColor.secondaryLabelColor; _selectionLabel.autoresizingMask = NSViewMinXMargin;
    _selectionLabel.stringValue = @"已选择 0 条";
    [dataContainer addSubview:_selectionLabel];

    // Dedicated selection row leaves the full width below it for packet details.
    NSStackView *selectionRow = Row(@[selectAll, clearSelection, invertSelection, analyzeSelected, aiAnalyze]);
    PinRow(selectionRow, dataContainer, 0);
    _selectionLabel.translatesAutoresizingMaskIntoConstraints = NO;
    _selectionLabel.font = [NSFont systemFontOfSize:11];
    _selectionLabel.lineBreakMode = NSLineBreakByTruncatingTail;
    _selectionLabel.maximumNumberOfLines = 1;
    detailScroll.translatesAutoresizingMaskIntoConstraints = NO;
    [NSLayoutConstraint activateConstraints:@[
        [_selectionLabel.leadingAnchor constraintEqualToAnchor:dataContainer.leadingAnchor constant:8],
        [_selectionLabel.trailingAnchor constraintEqualToAnchor:dataContainer.trailingAnchor constant:-8],
        [_selectionLabel.topAnchor constraintEqualToAnchor:selectionRow.bottomAnchor constant:4],
        [_selectionLabel.heightAnchor constraintEqualToConstant:20],
        [detailScroll.leadingAnchor constraintEqualToAnchor:dataContainer.leadingAnchor],
        [detailScroll.trailingAnchor constraintEqualToAnchor:dataContainer.trailingAnchor],
        [detailScroll.bottomAnchor constraintEqualToAnchor:dataContainer.bottomAnchor],
        [detailScroll.heightAnchor constraintEqualToConstant:56]
    ]];

    NSBox *detailSep = [[[NSBox alloc] initWithFrame:NSMakeRect(0, 56, 700, 1)] autorelease];
    detailSep.boxType = NSBoxSeparator; detailSep.autoresizingMask = NSViewWidthSizable;
    [dataContainer addSubview:detailSep];

    // 数据表格（91px 以上，随窗口伸缩）
    NSScrollView *dataScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(0, 91, 700, 243)] autorelease];
    dataScroll.borderType = NSBezelBorder; dataScroll.hasVerticalScroller = YES; dataScroll.hasHorizontalScroller = YES;
    dataScroll.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    dataScroll.translatesAutoresizingMaskIntoConstraints = NO;
    _dataTable = [[NSTableView alloc] initWithFrame:dataScroll.contentView.bounds];
    _dataTable.font = [NSFont monospacedSystemFontOfSize:12 weight:NSFontWeightRegular];
    _dataTable.usesAlternatingRowBackgroundColors = YES;
    _dataTable.allowsMultipleSelection = YES;
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
    NSTableColumn *tc5 = [[[NSTableColumn alloc] initWithIdentifier:@"protocol"] autorelease];
    tc5.title = @"协议"; tc5.width = 90; [_dataTable addTableColumn:tc5];
    NSTableColumn *tc6 = [[[NSTableColumn alloc] initWithIdentifier:@"status"] autorelease];
    tc6.title = @"状态"; tc6.width = 65; [_dataTable addTableColumn:tc6];
    NSTableColumn *tc7 = [[[NSTableColumn alloc] initWithIdentifier:@"response"] autorelease];
    tc7.title = @"响应时间"; tc7.width = 80; [_dataTable addTableColumn:tc7];
    dataScroll.documentView = _dataTable;
    _dataTable.rowHeight = 22;
    NSMenu *tableMenu = [[[NSMenu alloc] initWithTitle:@""] autorelease];
    [tableMenu addItemWithTitle:@"复制 HEX" action:@selector(copyPacketHex:) keyEquivalent:@""];
    [tableMenu addItemWithTitle:@"复制 ASCII" action:@selector(copyPacketASCII:) keyEquivalent:@""];
    [tableMenu addItemWithTitle:@"复制整行" action:@selector(copyPacketAll:) keyEquivalent:@""];
    [tableMenu addItemWithTitle:@"本地分析" action:@selector(analyzePacket:) keyEquivalent:@""];
    _dataTable.menu = tableMenu;
    [dataContainer addSubview:dataScroll];
    [NSLayoutConstraint activateConstraints:@[
        [dataScroll.leadingAnchor constraintEqualToAnchor:dataContainer.leadingAnchor],
        [dataScroll.trailingAnchor constraintEqualToAnchor:dataContainer.trailingAnchor],
        [dataScroll.topAnchor constraintEqualToAnchor:_selectionLabel.bottomAnchor constant:4],
        [dataScroll.bottomAnchor constraintEqualToAnchor:detailScroll.topAnchor constant:-6]
    ]];

    NSTabViewItem *dataItem = [[[NSTabViewItem alloc] initWithIdentifier:@"data"] autorelease];
    dataItem.label = @"接收数据"; dataItem.view = dataContainer;
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

    NSTabView *sendTabView = [[[NSTabView alloc] initWithFrame:NSMakeRect(320, 8, 700, 258)] autorelease];
    _sendView = sendTabView;
    sendTabView.autoresizingMask = NSViewWidthSizable;

    NSView *sendDataContainer = [[[NSView alloc] initWithFrame:NSMakeRect(0, 0, 700, 212)] autorelease];
    sendDataContainer.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    _hexSend = [[NSButton checkboxWithTitle:@"HEX 发送" target:nil action:nil] retain];
    _hexSend.state = NSControlStateValueOn;
    _hexSend.frame = NSMakeRect(0, 166, 100, 26); _hexSend.autoresizingMask = NSViewMinYMargin; [sendDataContainer addSubview:_hexSend];
    NSTextField *eolLabel = Label(@"行尾", NSMakeRect(110, 168, 36, 24));
    [sendDataContainer addSubview:eolLabel];
    _eol = [Combo(NSMakeRect(148, 164, 80, 30), @[@"无",@"LF",@"CR",@"CRLF"], @"无") retain];
    _eol.autoresizingMask = NSViewMinYMargin; [sendDataContainer addSubview:_eol];
    NSTextField *hint = Label(@"HEX 示例：01 03 00 00 00 02", NSMakeRect(246, 168, 280, 24));
    hint.textColor = NSColor.secondaryLabelColor; hint.autoresizingMask = NSViewMinYMargin; [sendDataContainer addSubview:hint];

    NSTextField *historyLabel = Label(@"历史", NSMakeRect(0, 132, 40, 24));
    [sendDataContainer addSubview:historyLabel];
    _sendHistory = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(40, 128, 140, 26) pullsDown:NO];
    _sendHistory.target = self; _sendHistory.action = @selector(sendHistorySelected:);
    [sendDataContainer addSubview:_sendHistory];
    NSTextField *favoriteLabel = Label(@"收藏", NSMakeRect(188, 132, 40, 24));
    [sendDataContainer addSubview:favoriteLabel];
    _favorites = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(228, 128, 140, 26) pullsDown:NO];
    _favorites.target = self; _favorites.action = @selector(favoriteSelected:);
    [sendDataContainer addSubview:_favorites];
    NSButton *favBtn = [NSButton buttonWithTitle:@"收藏当前" target:self action:@selector(saveFavorite:)];
    favBtn.frame = NSMakeRect(376, 126, 90, 28); [sendDataContainer addSubview:favBtn];
    NSButton *delBtn = [NSButton buttonWithTitle:@"删除" target:self action:@selector(deleteFavorite:)];
    delBtn.frame = NSMakeRect(470, 126, 70, 28); [sendDataContainer addSubview:delBtn];
    NSTextField *intervalLabel = Label(@"间隔(ms)", NSMakeRect(540, 132, 58, 24));
    [sendDataContainer addSubview:intervalLabel];
    _interval = [[NSTextField alloc] initWithFrame:NSMakeRect(598, 128, 102, 30)];
    _interval.stringValue = @"1000"; _interval.alignment = NSTextAlignmentRight;
    _interval.autoresizingMask = NSViewMinXMargin; [sendDataContainer addSubview:_interval];

    NSScrollView *sendScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(0, 8, 590, 114)] autorelease];
    sendScroll.borderType = NSBezelBorder; sendScroll.hasVerticalScroller = YES; sendScroll.autoresizingMask = NSViewWidthSizable;
    _send = [[NSTextView alloc] initWithFrame:sendScroll.contentView.bounds];
    _send.font = [NSFont monospacedSystemFontOfSize:13 weight:NSFontWeightRegular];
    _send.autoresizingMask = NSViewWidthSizable; sendScroll.documentView = _send; [sendDataContainer addSubview:sendScroll];
    NSButton *sendButton = [NSButton buttonWithTitle:@"发送一次" target:self action:@selector(send:)];
    sendButton.bezelColor = NSColor.systemBlueColor;
    sendButton.frame = NSMakeRect(605, 68, 95, 54); sendButton.autoresizingMask = NSViewMinXMargin; [sendDataContainer addSubview:sendButton];
    _quickTimerButton = [[NSButton buttonWithTitle:@"开始定时" target:self action:@selector(toggleTimer:)] retain];
    _quickTimerButton.frame = NSMakeRect(605, 8, 95, 54); _quickTimerButton.autoresizingMask = NSViewMinXMargin; [sendDataContainer addSubview:_quickTimerButton];

    PinRow(Row(@[_hexSend, eolLabel, _eol, intervalLabel, _interval, hint]), sendDataContainer, 4);
    PinRow(Row(@[historyLabel, _sendHistory, favoriteLabel, _favorites, favBtn, delBtn]), sendDataContainer, 40);
    [_eol.widthAnchor constraintEqualToConstant:75].active = YES;
    [_interval.widthAnchor constraintEqualToConstant:80].active = YES;
    [_sendHistory.widthAnchor constraintEqualToConstant:130].active = YES;
    [_favorites.widthAnchor constraintEqualToConstant:130].active = YES;
    // Long saved messages must not enlarge the popup or cover adjacent actions.
    [_sendHistory setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    [_favorites setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    NSStackView *sendActions = [NSStackView stackViewWithViews:@[sendButton, _quickTimerButton]];
    sendActions.orientation = NSUserInterfaceLayoutOrientationVertical;
    sendActions.alignment = NSLayoutAttributeWidth;
    sendActions.spacing = 8;
    sendActions.translatesAutoresizingMaskIntoConstraints = NO;
    sendScroll.translatesAutoresizingMaskIntoConstraints = NO;
    [sendDataContainer addSubview:sendActions];
    [NSLayoutConstraint activateConstraints:@[
        [sendActions.trailingAnchor constraintEqualToAnchor:sendDataContainer.trailingAnchor constant:-8],
        [sendActions.topAnchor constraintEqualToAnchor:sendDataContainer.topAnchor constant:80],
        [sendActions.widthAnchor constraintEqualToConstant:100],
        [sendScroll.leadingAnchor constraintEqualToAnchor:sendDataContainer.leadingAnchor constant:8],
        [sendScroll.trailingAnchor constraintEqualToAnchor:sendActions.leadingAnchor constant:-8],
        [sendScroll.topAnchor constraintEqualToAnchor:sendDataContainer.topAnchor constant:80],
        [sendScroll.bottomAnchor constraintEqualToAnchor:sendDataContainer.bottomAnchor constant:-8]
    ]];

    NSTabViewItem *sendDataItem = [[[NSTabViewItem alloc] initWithIdentifier:@"send"] autorelease];
    sendDataItem.label = @"发送数据"; sendDataItem.view = sendDataContainer;
    [sendTabView addTabViewItem:sendDataItem];

    NSView *timerContainer = [[[NSView alloc] initWithFrame:NSMakeRect(0, 0, 700, 212)] autorelease];
    timerContainer.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    _loopSend = [[NSButton checkboxWithTitle:@"循环" target:nil action:nil] retain];
    _loopSend.frame = NSMakeRect(0, 166, 52, 26); _loopSend.autoresizingMask = NSViewMinYMargin; [timerContainer addSubview:_loopSend];
    NSTextField *countLabel = Label(@"次数(0=一直)", NSMakeRect(58, 168, 92, 24));
    [timerContainer addSubview:countLabel];
    _loopCount = [[NSTextField alloc] initWithFrame:NSMakeRect(152, 164, 60, 28)];
    _loopCount.placeholderString = @"0=∞"; _loopCount.stringValue = @"0";
    _loopCount.autoresizingMask = NSViewMinYMargin; [timerContainer addSubview:_loopCount];
    NSTextField *timerHint = Label(@"间隔和发送内容已移至“发送数据”页", NSMakeRect(230, 168, 300, 24));
    timerHint.textColor = NSColor.secondaryLabelColor; [timerContainer addSubview:timerHint];
    _loopButton = [[NSButton buttonWithTitle:@"循环发送" target:self action:@selector(toggleLoop:)] retain];
    _loopButton.frame = NSMakeRect(0, 82, 150, 54); [timerContainer addSubview:_loopButton];
    _timerButton = [[NSButton buttonWithTitle:@"开始定时" target:self action:@selector(toggleTimer:)] retain];
    _timerButton.frame = NSMakeRect(160, 82, 150, 54); [timerContainer addSubview:_timerButton];
    PinRow(Row(@[_loopSend, countLabel, _loopCount, timerHint]), timerContainer, 4);
    [_loopCount.widthAnchor constraintEqualToConstant:65].active = YES;
    PinRow(Row(@[_loopButton, _timerButton]), timerContainer, 48);

    NSTabViewItem *timerItem = [[[NSTabViewItem alloc] initWithIdentifier:@"timer"] autorelease];
    timerItem.label = @"定时发送"; timerItem.view = timerContainer;
    [sendTabView addTabViewItem:timerItem];
    [view addSubview:sendTabView];

    NSBox *analysisSidebar = [[[NSBox alloc] initWithFrame:NSMakeRect(1045, 20, 215, 680)] autorelease];
    _analysisSidebar = analysisSidebar;
    analysisSidebar.title = @"分析中心";
    analysisSidebar.boxType = NSBoxPrimary;
    analysisSidebar.autoresizingMask = NSViewMinXMargin | NSViewHeightSizable;
    [view addSubview:analysisSidebar];
    NSView *analysisView = analysisSidebar.contentView;
    NSTextField *localTitle = Label(@"本地分析", NSMakeRect(16, 610, 180, 24));
    localTitle.font = [NSFont boldSystemFontOfSize:14]; [analysisView addSubview:localTitle];
    _analysisScope = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(16, 570, 180, 28) pullsDown:NO];
    [_analysisScope addItemsWithTitles:@[@"当前数据区", @"选中数据"]];
    _analysisScope.target = self; _analysisScope.action = @selector(updateAnalysisScope:);
    [analysisView addSubview:_analysisScope];
    _analysisStats = [[NSTextField alloc] initWithFrame:NSMakeRect(16, 510, 180, 50)];
    _analysisStats.editable = NO; _analysisStats.bordered = NO; _analysisStats.drawsBackground = NO;
    _analysisStats.font = [NSFont systemFontOfSize:11]; _analysisStats.textColor = NSColor.secondaryLabelColor;
    _analysisStats.usesSingleLineMode = NO; _analysisStats.maximumNumberOfLines = 3;
    ((NSTextFieldCell *)_analysisStats.cell).wraps = YES; [analysisView addSubview:_analysisStats];
    NSButton *localButton = [NSButton buttonWithTitle:@"开始本地分析" target:self action:@selector(runLocalAnalysis:)];
    localButton.frame = NSMakeRect(16, 470, 180, 32); localButton.bezelStyle = NSBezelStyleRounded; [analysisView addSubview:localButton];
    NSBox *aiSep = [[[NSBox alloc] initWithFrame:NSMakeRect(16, 455, 180, 1)] autorelease];
    aiSep.boxType = NSBoxSeparator; [analysisView addSubview:aiSep];
    NSTextField *aiTitle = Label(@"AI 增强分析", NSMakeRect(16, 420, 180, 24));
    aiTitle.font = [NSFont boldSystemFontOfSize:14]; [analysisView addSubview:aiTitle];
    _analysisAIStatus = [[NSTextField alloc] initWithFrame:NSMakeRect(16, 370, 180, 42)];
    _analysisAIStatus.editable = NO; _analysisAIStatus.bordered = NO; _analysisAIStatus.drawsBackground = NO;
    _analysisAIStatus.font = [NSFont systemFontOfSize:11]; _analysisAIStatus.textColor = NSColor.secondaryLabelColor;
    _analysisAIStatus.usesSingleLineMode = NO; _analysisAIStatus.maximumNumberOfLines = 2;
    ((NSTextFieldCell *)_analysisAIStatus.cell).wraps = YES; [analysisView addSubview:_analysisAIStatus];
    NSButton *aiButton = [NSButton buttonWithTitle:@"AI 深度分析" target:self action:@selector(runAIAnalysis:)];
    aiButton.frame = NSMakeRect(16, 330, 180, 32); aiButton.bezelStyle = NSBezelStyleRounded; [analysisView addSubview:aiButton];
    NSButton *settingsButton = [NSButton buttonWithTitle:@"AI 设置" target:self action:@selector(openAISettings:)];
    settingsButton.frame = NSMakeRect(16, 295, 180, 28); settingsButton.bezelStyle = NSBezelStyleRounded; [analysisView addSubview:settingsButton];
    NSScrollView *analysisScroll = [[[NSScrollView alloc] initWithFrame:NSMakeRect(16, 20, 180, 260)] autorelease];
    analysisScroll.borderType = NSBezelBorder; analysisScroll.hasVerticalScroller = YES;
    _analysisResult = [[NSTextView alloc] initWithFrame:analysisScroll.contentView.bounds];
    _analysisResult.editable = NO; _analysisResult.font = [NSFont monospacedSystemFontOfSize:11 weight:NSFontWeightRegular];
    _analysisResult.autoresizingMask = NSViewWidthSizable; analysisScroll.documentView = _analysisResult;
    [analysisView addSubview:analysisScroll];
    NSButton *databaseButton = [NSButton buttonWithTitle:@"分析数据库…" target:self action:@selector(openDatabaseAnalysis:)];
    NSStackView *analysisHeader = [NSStackView stackViewWithViews:@[localTitle, _analysisScope, _analysisStats, localButton, databaseButton, aiSep, aiTitle, _analysisAIStatus, aiButton, settingsButton]];
    analysisHeader.orientation = NSUserInterfaceLayoutOrientationVertical;
    analysisHeader.alignment = NSLayoutAttributeLeading;
    analysisHeader.spacing = 10;
    analysisHeader.translatesAutoresizingMaskIntoConstraints = NO;
    analysisScroll.translatesAutoresizingMaskIntoConstraints = NO;
    [analysisView addSubview:analysisHeader];
    for (NSView *item in analysisHeader.views) {
        [item.widthAnchor constraintEqualToAnchor:analysisHeader.widthAnchor].active = YES;
    }
    _analysisResult.string = @"选择分析范围后，点击“开始本地分析”。\n\nAI 增强分析默认关闭。";
    [NSLayoutConstraint activateConstraints:@[
        [analysisHeader.topAnchor constraintEqualToAnchor:analysisView.topAnchor constant:12],
        [analysisHeader.leadingAnchor constraintEqualToAnchor:analysisView.leadingAnchor constant:12],
        [analysisHeader.trailingAnchor constraintEqualToAnchor:analysisView.trailingAnchor constant:-12],
        [_analysisStats.heightAnchor constraintEqualToConstant:52],
        [_analysisAIStatus.heightAnchor constraintEqualToConstant:44],
        [aiSep.heightAnchor constraintEqualToConstant:1],
        [analysisScroll.topAnchor constraintEqualToAnchor:analysisHeader.bottomAnchor constant:12],
        [analysisScroll.leadingAnchor constraintEqualToAnchor:analysisHeader.leadingAnchor],
        [analysisScroll.trailingAnchor constraintEqualToAnchor:analysisHeader.trailingAnchor],
        [analysisScroll.bottomAnchor constraintEqualToAnchor:analysisView.bottomAnchor constant:-12]
    ]];
    [self updateAnalysisScope:nil];

    _leftPane = [[[NSView alloc] initWithFrame:NSMakeRect(0, 0, 310, view.bounds.size.height)] autorelease];
    _centerPane = [[[NSView alloc] initWithFrame:NSMakeRect(310, 0, 710, view.bounds.size.height)] autorelease];
    _rightPane = [[[NSView alloc] initWithFrame:NSMakeRect(1025, 0, 255, view.bounds.size.height)] autorelease];
    _leftPane.autoresizingMask = NSViewMinYMargin;
    _centerPane.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    _rightPane.autoresizingMask = NSViewMinXMargin | NSViewHeightSizable;
    [view addSubview:_leftPane]; [view addSubview:_centerPane]; [view addSubview:_rightPane];
    NSArray *legacyViews = [[view.subviews copy] autorelease];
    for (NSView *child in legacyViews) {
        if (child == _leftPane || child == _centerPane || child == _rightPane) continue;
        NSRect frame = child.frame;
        if (child == _analysisSidebar) {
            frame.origin.x -= 1025.0; [_rightPane addSubview:child];
        } else if (frame.origin.x < 310.0) {
            [_leftPane addSubview:child];
        } else {
            frame.origin.x -= 310.0; [_centerPane addSubview:child];
        }
        child.frame = frame;
    }
    // A compact form keeps mode-dependent fields in one flow, without empty slots.
    [config removeFromSuperview];
    NSStackView *portRow = Row(@[_ports, _refresh]);
    [_ports setContentCompressionResistancePriority:NSLayoutPriorityDefaultLow forOrientation:NSLayoutConstraintOrientationHorizontal];
    [_ports.widthAnchor constraintEqualToAnchor:portRow.widthAnchor constant:-64].active = YES;
    [_refresh.widthAnchor constraintEqualToConstant:56].active = YES;
    _endpointForm = Field(_endpointLabel, portRow);
    _serialForm = Column(@[FormPair(Field(baudLabel, _baud), Field(dataLabel, _data)), FormPair(Field(parityLabel, _parity), Field(stopLabel, _stop))], 12);
    _protocolField = Field(_protocolLabel, _bridgeProtocol);
    _networkForm = FormPair(_protocolField, Field(_roleLabel, _role));
    _portField = Field(_portLabel, _port);
    _addressForm = FormPair(Field(_ipLabel, _ip), _portField);
    _status.alignment = NSTextAlignmentLeft;
    _status.font = [NSFont systemFontOfSize:13 weight:NSFontWeightSemibold];
    _status.usesSingleLineMode = YES;
    _status.maximumNumberOfLines = 1;
    _status.lineBreakMode = NSLineBreakByTruncatingTail;
    [_status.heightAnchor constraintEqualToConstant:20].active = YES;
    _connect.bezelColor = NSColor.systemBlueColor;
    _connect.controlSize = NSControlSizeLarge;
    _connectionDetail = StatusLines(2);
    _connectionDetail.textColor = NSColor.secondaryLabelColor;
    _connectionMetrics = StatusLines(4);
    _connectionMetrics.font = [NSFont monospacedDigitSystemFontOfSize:12 weight:NSFontWeightRegular];
    _peerInfo = StatusLines(4);
    addressHint.stringValue = @"选择通信模式，配置参数后连接";
    addressHint.alignment = NSTextAlignmentLeft;
    _connectionCards = Column(@[
        Card(@"连接配置", @[Field(modeLabel, _mode), _endpointForm, _serialForm, _networkForm, _addressForm, _connect]),
        Card(@"连接状态", @[_status, _connectionDetail, _connectionMetrics]),
        Card(@"对端信息", @[_peerInfo]),
        Card(@"快捷操作", @[_history, addressHint])
    ], 10);
    _connectionScroll = [[[NSScrollView alloc] initWithFrame:_leftPane.bounds] autorelease];
    _connectionScroll.hasVerticalScroller = YES;
    _connectionScroll.automaticallyAdjustsContentInsets = NO;
    _connectionScroll.drawsBackground = NO;
    NSView *document = [[[NSView alloc] initWithFrame:_leftPane.bounds] autorelease];
    document.autoresizesSubviews = NO;
    _connectionCards.translatesAutoresizingMaskIntoConstraints = NO;
    [document addSubview:_connectionCards];
    _connectionWidth = [_connectionCards.widthAnchor constraintEqualToConstant:290];
    [NSLayoutConstraint activateConstraints:@[
        _connectionWidth,
        [_connectionCards.leadingAnchor constraintEqualToAnchor:document.leadingAnchor constant:10],
        [_connectionCards.topAnchor constraintEqualToAnchor:document.topAnchor constant:10]
    ]];
    _connectionScroll.documentView = document;
    [_leftPane addSubview:_connectionScroll];
    _window.contentView = view;
    view.autoresizesSubviews = NO;
    [_window setContentSize:NSMakeSize(1280, 820)];
    PinRow(_filterRow, _centerPane, 60);
    [self layoutMainPanes];

    if (getenv("COMMBOX_LAYOUT_CHECK_DIR")) {
        exit(RunLayoutChecks(self, [NSString stringWithUTF8String:getenv("COMMBOX_LAYOUT_CHECK_DIR")]));
    }

    [_window makeKeyAndOrderFront:nil];
    [NSApp activateIgnoringOtherApps:YES];
    [self modeChanged:nil];
    [self refreshStats:nil];
    [self startStatsTimer];
    [self reloadHistory];
    [self refreshSendHistory];
    [self refreshFavorites];
    char *database = GoDatabaseInfo();
    NSString *databaseInfo = [NSString stringWithUTF8String:database ?: ""]; free(database);
    if ([databaseInfo hasPrefix:@"错误:"]) [self alert:databaseInfo];
    else [self appendText:[NSString stringWithFormat:@"[SQLite 数据目录：%@]\n", databaseInfo]];
}

- (void)windowDidResize:(NSNotification *)notification {
    if ([notification object] != _window) return;
    [self layoutMainPanes];
}

- (void)layoutMainPanes {
    if (!_leftPane) return;
    CGFloat width = _window.contentView.bounds.size.width;
    CGFloat height = _window.contentView.bounds.size.height;
    if (width < 1280) _analysisVisible = NO;
    _rightPane.hidden = !_analysisVisible;
    CGFloat rightX = width - (_analysisVisible ? 255.0 : 0);
    CGFloat centerWidth = MAX(710.0, rightX - 315.0);
    _leftPane.frame = NSMakeRect(0, 0, 310, height);
    _centerPane.frame = NSMakeRect(310, 0, centerWidth, height);
    _rightPane.frame = NSMakeRect(rightX, 0, 255, height);
    _dataView.frame = NSMakeRect(10, 204, centerWidth - 10, height - 294);
    _sendView.frame = NSMakeRect(10, 8, centerWidth - 10, 190);
    _sendBackground.frame = NSMakeRect(0, 0, centerWidth, 200);
    _horizontalSeparator.frame = NSMakeRect(0, 200, centerWidth, 1);
    _analysisSidebar.frame = NSMakeRect(20, 20, 215, height - 40);
    [self layoutConnectionPane];
}

- (void)layoutConnectionPane {
    if (!_connectionScroll) return;
    _connectionScroll.frame = _leftPane.bounds;
    CGFloat width = _connectionScroll.contentSize.width;
    _connectionWidth.constant = width - 20;
    [_connectionScroll.documentView layoutSubtreeIfNeeded];
    CGFloat cardHeight = _connectionCards.fittingSize.height;
    CGFloat height = MAX(_connectionScroll.contentSize.height, cardHeight + 20);
    _connectionScroll.documentView.frame = NSMakeRect(0, 0, width, height);
    [_connectionScroll.documentView layoutSubtreeIfNeeded];
    [_connectionScroll.documentView scrollPoint:NSMakePoint(0, height)];
}

- (NSArray *)toolbarAllowedItemIdentifiers:(NSToolbar *)toolbar {
    return @[@"new", @"clear", @"export", @"analysis", @"database", NSToolbarFlexibleSpaceItemIdentifier, NSToolbarSpaceItemIdentifier];
}

- (NSArray *)toolbarDefaultItemIdentifiers:(NSToolbar *)toolbar {
    return @[@"new", NSToolbarSpaceItemIdentifier, @"clear", @"export", NSToolbarFlexibleSpaceItemIdentifier, @"analysis", @"database"];
}

- (NSToolbarItem *)toolbar:(NSToolbar *)toolbar itemForItemIdentifier:(NSString *)identifier willBeInsertedIntoToolbar:(BOOL)flag {
    NSString *label = nil;
    NSString *imageName = nil;
    SEL action = NULL;
    if ([identifier isEqualToString:@"new"]) { label = @"新建"; imageName = NSImageNameAddTemplate; action = @selector(newInstance:); }
    else if ([identifier isEqualToString:@"clear"]) { label = @"清空"; imageName = NSImageNameRemoveTemplate; action = @selector(clear:); }
    else if ([identifier isEqualToString:@"export"]) { label = @"导出"; imageName = NSImageNameShareTemplate; action = @selector(exportLog:); }
    else if ([identifier isEqualToString:@"analysis"]) { label = @"分析中心"; imageName = NSImageNameAdvanced; action = @selector(toggleAnalysisCenter:); }
    else if ([identifier isEqualToString:@"database"]) { label = @"数据库分析"; imageName = NSImageNameFolder; action = @selector(openDatabaseAnalysis:); }
    else return nil;
    NSToolbarItem *item = [[[NSToolbarItem alloc] initWithItemIdentifier:identifier] autorelease];
    item.label = label; item.paletteLabel = label; item.toolTip = label;
    item.image = [NSImage imageNamed:imageName];
    item.target = self; item.action = action;
    return item;
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
    Item(actionMenu, @"AI 增强分析设置", @selector(openAISettings:), @"i", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    Submenu(mainMenu, @"操作", actionMenu);

    NSMenu *editMenu = [[[NSMenu alloc] initWithTitle:@"编辑"] autorelease];
    Item(editMenu, @"剪切", @selector(cut:), @"x", NSEventModifierFlagCommand);
    Item(editMenu, @"复制", @selector(copy:), @"c", NSEventModifierFlagCommand);
    Item(editMenu, @"粘贴", @selector(paste:), @"v", NSEventModifierFlagCommand);
    Item(editMenu, @"全选", @selector(selectAll:), @"a", NSEventModifierFlagCommand);
    Submenu(mainMenu, @"编辑", editMenu);

    NSMenu *viewMenu = [[[NSMenu alloc] initWithTitle:@"视图"] autorelease];
    Item(viewMenu, @"打开数据库目录", @selector(revealDatabase:), @"d", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    Item(viewMenu, @"清空接收区", @selector(clear:), @"k", NSEventModifierFlagCommand);
    Item(viewMenu, @"导出接收数据", @selector(exportLog:), @"e", NSEventModifierFlagCommand);
    Item(viewMenu, @"ASCII 列开关", @selector(toggleHexView:), @"h", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    Item(viewMenu, @"监控窗口", @selector(openMonitor:), @"m", NSEventModifierFlagCommand | NSEventModifierFlagShift);
    Submenu(mainMenu, @"视图", viewMenu);

    NSApp.mainMenu = mainMenu;
}

- (void)openAISettings:(id)sender {
    if (!_aiSettingsWindow) {
        _aiSettingsWindow = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 480, 250)
            styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
            backing:NSBackingStoreBuffered defer:NO];
        _aiSettingsWindow.title = @"AI 增强分析";
        _aiSettingsWindow.releasedWhenClosed = NO;
        NSView *v = _aiSettingsWindow.contentView;
        _aiEnabledButton = [NSButton checkboxWithTitle:@"启用 DeepSeek（默认关闭）" target:self action:@selector(aiEnabledChanged:)];
        _aiEnabledButton.frame = NSMakeRect(24, 198, 260, 26); [v addSubview:_aiEnabledButton];
        [v addSubview:Label(@"API Base URL", NSMakeRect(24, 158, 100, 22))];
        _aiBaseURL = [[NSTextField alloc] initWithFrame:NSMakeRect(130, 154, 320, 28)]; [v addSubview:_aiBaseURL];
        [v addSubview:Label(@"模型", NSMakeRect(24, 118, 100, 22))];
        _aiModel = [[NSTextField alloc] initWithFrame:NSMakeRect(130, 114, 320, 28)]; [v addSubview:_aiModel];
        [v addSubview:Label(@"API Key", NSMakeRect(24, 78, 100, 22))];
        _aiKey = [[NSSecureTextField alloc] initWithFrame:NSMakeRect(130, 74, 320, 28)]; _aiKey.placeholderString = @"留空表示不修改已保存 Key"; [v addSubview:_aiKey];
        _aiKeyHint = Label(@"", NSMakeRect(24, 42, 420, 22)); _aiKeyHint.textColor = NSColor.secondaryLabelColor; [v addSubview:_aiKeyHint];
        NSButton *save = [NSButton buttonWithTitle:@"保存" target:self action:@selector(saveAISettings:)]; save.frame = NSMakeRect(370, 12, 80, 28); [v addSubview:save];
        [_aiSettingsWindow center];
    }
    // 每次打开都重新读取已保存设置，避免复用窗口时显示旧状态。
    char *enabledRaw = GoGetAISetting((char *)"deepseek.enabled"); BOOL savedEnabled = [[NSString stringWithUTF8String:enabledRaw ?: ""] isEqualToString:@"true"]; free(enabledRaw);
    char *keyRaw = GoGetAISetting((char *)"deepseek.api_key"); BOOL hasKey = strlen(keyRaw ?: "") > 0; free(keyRaw);
    char *baseURLRaw = GoGetAISetting((char *)"deepseek.base_url"); NSString *savedURL = [NSString stringWithUTF8String:baseURLRaw ?: ""]; free(baseURLRaw);
    char *modelRaw = GoGetAISetting((char *)"deepseek.model"); NSString *savedModel = [NSString stringWithUTF8String:modelRaw ?: ""]; free(modelRaw);
    _aiEnabledButton.state = savedEnabled ? NSControlStateValueOn : NSControlStateValueOff;
    _aiEnabled = savedEnabled;
    _aiBaseURL.stringValue = savedURL.length ? savedURL : @"https://api.deepseek.com";
    _aiModel.stringValue = savedModel.length ? savedModel : @"deepseek-chat";
    _aiKey.stringValue = @"";
    _aiKeyHint.stringValue = hasKey ? @"Key 已保存到本地 SQLite；未启用时不会发起网络请求。" : @"Key 为空；保存后仅写入本地 SQLite，不会自动调用。";
    [_aiSettingsWindow makeKeyAndOrderFront:nil];
}

- (void)aiEnabledChanged:(NSButton *)sender { _aiEnabled = sender.state == NSControlStateValueOn; }

- (void)saveAISettings:(id)sender {
    NSString *baseURL = [_aiBaseURL.stringValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
    NSString *model = [_aiModel.stringValue stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
    NSURL *url = [NSURL URLWithString:baseURL];
    if (!url || !url.host.length || !([url.scheme.lowercaseString isEqualToString:@"http"] || [url.scheme.lowercaseString isEqualToString:@"https"])) { [self alert:@"API Base URL 无效，请使用 http:// 或 https:// 地址"]; return; }
    if (!model.length) { [self alert:@"模型不能为空"]; return; }
    char *error = GoSetAISetting((char *)"deepseek.enabled", (char *)(_aiEnabled ? "true" : "false"));
    if (strlen(error ?: "") > 0) { NSString *message = [NSString stringWithUTF8String:error]; free(error); [self alert:message]; return; } free(error);
    error = GoSetAISetting((char *)"deepseek.base_url", (char *)baseURL.UTF8String); free(error);
    error = GoSetAISetting((char *)"deepseek.model", (char *)model.UTF8String); free(error);
    if (_aiKey.stringValue.length) {
        error = GoSetAISetting((char *)"deepseek.api_key", (char *)_aiKey.stringValue.UTF8String); free(error);
        _aiKey.stringValue = @"";
    }
    [self appendText:@"[AI 设置已保存到本地 SQLite；DeepSeek 仅在用户主动分析时调用]\n"];
    [_aiSettingsWindow orderOut:nil];
}

- (NSArray *)analysisPackets {
    if ([_analysisScope.titleOfSelectedItem isEqualToString:@"选中数据"]) {
        NSMutableArray *selected = [NSMutableArray array];
        [_dataTable.selectedRowIndexes enumerateIndexesUsingBlock:^(NSUInteger idx, BOOL *stop) {
            [selected addObject:_visiblePackets[idx]];
        }];
        return selected;
    }
    return _visiblePackets;
}

- (void)updateAnalysisScope:(id)sender {
    NSArray *packets = [self analysisPackets];
    NSInteger rx = 0, tx = 0, bytes = 0;
    for (NSDictionary *p in packets) {
        if ([p[@"dir"] isEqualToString:@"RX"]) rx++; else if ([p[@"dir"] isEqualToString:@"TX"]) tx++;
        bytes += [p[@"rawLen"] integerValue];
    }
    _analysisStats.stringValue = [NSString stringWithFormat:@"记录：%ld · %ld B\nRX：%ld · TX：%ld\n范围：%@",
        (long)packets.count, (long)bytes, (long)rx, (long)tx, _analysisScope.titleOfSelectedItem];
    char *enabled = GoGetAISetting((char *)"deepseek.enabled");
    char *key = GoGetAISetting((char *)"deepseek.api_key");
    BOOL ready = strcmp(enabled ?: "", "true") == 0 && strlen(key ?: "") > 0;
    _analysisAIStatus.stringValue = ready ? @"AI 状态：可用（点击后仍需确认发送范围）" : (strcmp(enabled ?: "", "true") == 0 ? @"AI 状态：Key 未配置" : @"AI 状态：未启用，本地分析可用");
    free(enabled); free(key);
}

- (void)openAnalysisCenter:(id)sender {
    _analysisVisible = YES;
    if (_window.contentView.bounds.size.width < 1280) [_window setContentSize:NSMakeSize(1280, _window.contentView.bounds.size.height)];
    [self layoutMainPanes];
    [self updateAnalysisScope:nil];
    [_window makeKeyAndOrderFront:nil];
    [_window makeFirstResponder:_analysisScope];
}

- (void)toggleAnalysisCenter:(id)sender {
    if (!_analysisVisible) { [self openAnalysisCenter:sender]; return; }
    _analysisVisible = NO;
    [self layoutMainPanes];
}

- (NSAlert *)databaseAnalysisDialog:(NSArray *)files {
    NSAlert *dialog = [[[NSAlert alloc] init] autorelease];
    dialog.messageText = @"分析数据库中的通信数据";
    dialog.informativeText = @"合并只读查询所选捕获文件，不上传数据。最近条数为所有文件合计上限，最高 100 万；负载最多 8 MiB，详细展开最多 20 条，省略数量会明确提示。";
    [dialog addButtonWithTitle:@"开始本地分析"];
    [dialog addButtonWithTitle:@"取消"];
    DatabaseFilePicker *database = [[[DatabaseFilePicker alloc] initWithFilenames:files] autorelease];
    [database.heightAnchor constraintEqualToConstant:104].active = YES;
    NSButton *allFiles = [NSButton buttonWithTitle:@"全选文件" target:database.documentView action:@selector(selectAll:)];
    NSButton *noFiles = [NSButton buttonWithTitle:@"取消选择" target:database.documentView action:@selector(deselectAll:)];
    NSDatePicker *start = [[[NSDatePicker alloc] initWithFrame:NSZeroRect] autorelease];
    NSDatePicker *end = [[[NSDatePicker alloc] initWithFrame:NSZeroRect] autorelease];
    for (NSDatePicker *picker in @[start, end]) {
        picker.datePickerStyle = NSDatePickerStyleTextFieldAndStepper;
        picker.datePickerElements = NSDatePickerElementFlagYearMonthDay | NSDatePickerElementFlagHourMinuteSecond;
        picker.timeZone = NSTimeZone.localTimeZone;
    }
    start.dateValue = [NSDate dateWithTimeIntervalSinceNow:-86400]; start.tag = 102;
    end.dateValue = [NSDate date]; end.tag = 103;
    NSPopUpButton *direction = [[[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO] autorelease];
    [direction addItemsWithTitles:@[@"全部方向", @"RX", @"TX"]]; direction.tag = 104;
    NSPopUpButton *count = [[[NSPopUpButton alloc] initWithFrame:NSZeroRect pullsDown:NO] autorelease];
    for (NSNumber *limit in @[@100, @500, @2000, @10000, @100000, @1000000]) {
        [count addItemWithTitle:[NSString stringWithFormat:@"最近 %@ 条", limit]];
        count.lastItem.representedObject = limit;
    }
    count.tag = 105;
    NSStackView *form = Column(@[
        Field(Label(@"数据库文件（⌘ / Shift 多选）", NSZeroRect), database),
        Row(@[allFiles, noFiles]),
        Field(Label(@"开始时间（本地时间）", NSZeroRect), start),
        Field(Label(@"结束时间（本地时间）", NSZeroRect), end),
        FormPair(Field(Label(@"方向", NSZeroRect), direction), Field(Label(@"最近条数（所选文件合计）", NSZeroRect), count))
    ], 12);
    [form.widthAnchor constraintEqualToConstant:420].active = YES;
    // NSAlert sizes its accessory host from the frame before running Auto Layout.
    form.frame = NSMakeRect(0, 0, 420, form.fittingSize.height);
    dialog.accessoryView = form;
    return dialog;
}

- (void)openDatabaseAnalysis:(id)sender {
    if (_databaseBusy) { [self alert:@"数据库正在本地分析，请稍候。\n查询最多等待 30 秒。"] ; return; }
    char *raw = GoListAnalysisDatabases();
    NSString *json = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    NSDictionary *result = [NSJSONSerialization JSONObjectWithData:[json dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
    if (!result || [result[@"error"] length]) { [self alert:result[@"error"] ?: @"无法读取数据库列表"]; return; }
    NSArray *files = result[@"files"];
    if (!files.count) { [self alert:@"尚无通信数据库。请先建立连接并收发数据。"] ; return; }
    NSAlert *dialog = [self databaseAnalysisDialog:files];
    [dialog beginSheetModalForWindow:_window completionHandler:^(NSModalResponse response) {
        if (response != NSAlertFirstButtonReturn) return;
        NSView *form = dialog.accessoryView;
        NSDatePicker *start = (NSDatePicker *)[form viewWithTag:102];
        NSDatePicker *end = (NSDatePicker *)[form viewWithTag:103];
        if ([start.dateValue compare:end.dateValue] == NSOrderedDescending) { [self alert:@"开始时间不能晚于结束时间"]; return; }
        NSArray *filenames = [(DatabaseFilePicker *)[form viewWithTag:101] selectedFilenames];
        if (!filenames.count) { [self alert:@"请至少选择一个数据库文件"]; return; }
        NSData *fileData = [NSJSONSerialization dataWithJSONObject:filenames options:0 error:nil];
        NSString *filesJSON = [[[NSString alloc] initWithData:fileData encoding:NSUTF8StringEncoding] autorelease];
        NSInteger directionIndex = [(NSPopUpButton *)[form viewWithTag:104] indexOfSelectedItem];
        NSString *direction = @[@"ALL", @"RX", @"TX"][directionIndex];
        int limit = [[(NSPopUpButton *)[form viewWithTag:105] selectedItem].representedObject intValue];
        NSISO8601DateFormatter *formatter = [[[NSISO8601DateFormatter alloc] init] autorelease];
        NSString *from = [formatter stringFromDate:start.dateValue];
        NSString *to = [formatter stringFromDate:end.dateValue];
        _databaseBusy = YES;
        [self openAnalysisCenter:nil];
        _analysisResult.string = @"正在只读查询数据库并进行本地分析……\n通信与定时发送不受影响。";
        dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{
            char *report = GoAnalyzeDatabases((char *)filesJSON.UTF8String, (char *)from.UTF8String, (char *)to.UTF8String, (char *)direction.UTF8String, limit);
            NSString *text = [[NSString alloc] initWithUTF8String:report ?: "数据库分析失败"]; free(report);
            dispatch_async(dispatch_get_main_queue(), ^{
                _databaseBusy = NO;
                _analysisResult.string = text;
                [text release];
            });
        });
    }];
}

- (void)runLocalAnalysis:(id)sender {
    [self updateAnalysisScope:nil];
    NSArray *packets = [self analysisPackets];
    if (!packets.count) { _analysisResult.string = @"没有可分析的数据。"; return; }
    NSMutableString *report = [NSMutableString stringWithFormat:@"范围统计\n%@\n\n详细报文分析\n", _analysisStats.stringValue];
    NSUInteger limit = MIN((NSUInteger)20, packets.count);
    for (NSUInteger i = 0; i < limit; i++) {
        NSDictionary *p = packets[i];
        char *raw = GoAnalyzePacket((char *)[_mode.titleOfSelectedItem UTF8String], (char *)[p[@"hex"] UTF8String]);
        [report appendFormat:@"\n[%lu] %@ %@ %@\nHEX：%@\n%@\n", (unsigned long)(i + 1), p[@"ts"] ?: @"", p[@"dir"] ?: @"", p[@"len"] ?: @"", p[@"hex"] ?: @"", [NSString stringWithUTF8String:raw ?: "分析失败"]];
        free(raw);
    }
    if (packets.count > limit) [report appendFormat:@"\n其余 %ld 条未展开，范围统计仍包含全部数据。", (long)(packets.count - limit)];
    _analysisResult.string = report;
}

- (void)runAIAnalysis:(NSButton *)sender {
    NSArray *packets = [self analysisPackets];
    if (!packets.count) { _analysisResult.string = @"没有可分析的数据。"; return; }
    NSMutableString *input = [NSMutableString string];
    for (NSDictionary *p in packets) [input appendFormat:@"%@ %@\n", p[@"dir"] ?: @"", p[@"hex"] ?: @""];
    char *enabled = GoGetAISetting((char *)"deepseek.enabled");
    char *key = GoGetAISetting((char *)"deepseek.api_key");
    BOOL ready = strcmp(enabled ?: "", "true") == 0 && strlen(key ?: "") > 0;
    free(enabled); free(key);
    if (!ready) { [self alert:@"AI 未启用或 Key 未配置，请先打开“操作 → AI 增强分析设置”"]; return; }
    NSString *preview = [input substringToIndex:MIN((NSUInteger)600, input.length)];
    NSAlert *confirm = [[[NSAlert alloc] init] autorelease];
    confirm.messageText = @"确认发送到 DeepSeek？";
    confirm.informativeText = [NSString stringWithFormat:@"将发送当前范围的 %ld 条报文，预览（最多 600 字符）：\n%@\n\n不会发送 IP、设备名或主机名。", (long)packets.count, preview];
    [confirm addButtonWithTitle:@"确认并发送"]; [confirm addButtonWithTitle:@"取消"];
    if ([confirm runModal] != NSAlertFirstButtonReturn) return;
    NSString *transport = [_mode.titleOfSelectedItem copy]; sender.enabled = NO; _analysisResult.string = @"AI 分析请求中……\n\n本地通信不会被阻塞。";
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{
        char *raw = GoAIAnalyze((char *)transport.UTF8String, (char *)input.UTF8String);
        NSString *result = [[NSString alloc] initWithUTF8String:raw ?: "AI 分析失败"]; free(raw);
        dispatch_async(dispatch_get_main_queue(), ^{ _analysisResult.string = [NSString stringWithFormat:@"%@\n\n%@", _analysisStats.stringValue, result]; sender.enabled = YES; [result release]; [transport release]; });
    });
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
    NSDictionary *stats = [NSJSONSerialization JSONObjectWithData:[text dataUsingEncoding:NSUTF8StringEncoding] options:0 error:nil];
    if ([stats isKindOfClass:NSDictionary.class]) [self applyConnectionStats:stats];
}

- (void)applyConnectionStats:(NSDictionary *)stats {
    NSInteger state = [stats[@"state"] integerValue];
    BOOL ready = state == 2, listening = [stats[@"listening"] boolValue];
    NSString *mode = stats[@"mode"] ?: @"";
    BOOL datagram = [stats[@"datagram"] boolValue] || [mode containsString:@"UDP"];
    NSString *title = @"● 未连接";
    NSColor *color = NSColor.secondaryLabelColor;
    if (ready) {
        title = listening ? @"● 监听中" : (datagram || [mode containsString:@"HTTP"] ? @"● 已就绪" : @"● 已连接");
        color = NSColor.systemGreenColor;
    } else if (state == 1 || state == 3) {
        title = state == 1 ? @"● 正在连接…" : @"● 正在重连…"; color = NSColor.systemOrangeColor;
    } else if (state == 5) { title = @"● 连接失败"; color = NSColor.systemRedColor; }
    else if (state == 4) { title = @"● 正在断开…"; }
    [self setStatus:title color:color];
    NSString *endpoint = stats[@"endpoint"] ?: @"";
    if (ready && ([mode containsString:@"串口"] || [mode isEqualToString:@"Serial"])) {
        endpoint = [NSString stringWithFormat:@"%@ %@", _ports.stringValue, endpoint];
        mode = [NSString stringWithFormat:@"%@ · %@ / %@ / %@ / %@", mode, _baud.stringValue, _data.stringValue, _parity.stringValue, _stop.stringValue];
    }
    _connectionDetail.stringValue = ready || state == 3 ? [NSString stringWithFormat:@"%@\n%@", mode, endpoint] : @"配置参数后建立连接\n统计为本次应用运行累计";
    _connectionDetail.toolTip = _connectionDetail.stringValue;
    NSString *elapsed = ready || state == 3 ? (stats[@"elapsed"] ?: @"—") : @"—";
    _connectionMetrics.stringValue = [NSString stringWithFormat:@"累计 RX  %@\n累计 TX  %@\n运行时间  %@\n重连  %@    错误  %@", stats[@"rx"] ?: @"0 条 · 0 B", stats[@"tx"] ?: @"0 条 · 0 B", elapsed, stats[@"reconnects"] ?: @0, stats[@"errors"] ?: @0];
    _connectionMetrics.toolTip = _connectionMetrics.stringValue;
    NSArray *peers = [stats[@"peers"] isKindOfClass:NSArray.class] ? stats[@"peers"] : @[];
    NSInteger count = [stats[@"peer_count"] integerValue];
    if (ready && count) {
        _peerInfo.stringValue = [NSString stringWithFormat:@"%ld 个 TCP 对端%@\n%@", (long)count, count > 3 ? @"（显示前 3 个）" : @"", [peers componentsJoinedByString:@"\n"]];
    } else {
        _peerInfo.stringValue = !ready ? @"尚未建立连接" : (listening ? @"等待客户端连接\n监听已启动，尚无客户端" : (datagram ? @"UDP 为无连接通信\n就绪不代表对端在线" : ([mode containsString:@"HTTP"] ? @"等待发送 HTTP 请求\n就绪不代表服务可达" : @"串口设备已打开")));
    }
    _peerInfo.toolTip = _peerInfo.stringValue;
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
    if ([_loopButton.title isEqualToString:@"停止循环"]) {
        char *raw = GoToggleLoop((char *)"", 0, (char *)"无", 0, 10);
        free(raw);
        [self loopDone];
    }
    _connected = NO;
    _mode.enabled = YES;
    [self setStatus:@"● 未连接" color:NSColor.secondaryLabelColor];
    [self modeChanged:nil];
    [self refreshStats:nil];
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
    if (message.length) { [self refreshStats:nil]; [self alert:message]; return; }

    _connected = YES; _mode.enabled = NO;
    _ports.enabled = NO; _refresh.enabled = NO; _ip.enabled = NO; _port.enabled = NO;
    _role.enabled = NO; _bridgeProtocol.enabled = NO;
    for (NSControl *control in _serialControls) control.enabled = NO;
    _connect.title = server ? @"停止监听" : @"断开";
    NSString *modeDesc = net ? [NSString stringWithFormat:@"%@ %@", mode, _role.titleOfSelectedItem] : mode;
    [self refreshStats:nil];
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
    if ([_loopButton.title isEqualToString:@"停止循环"]) { [self alert:@"请先停止循环发送"]; return; }
    if (!_send.string.length && (_hexSend.state == NSControlStateValueOn || [_eol.stringValue isEqualToString:@"无"])) {
        [self alert:@"请输入要发送的数据"]; return;
    }
    NSInteger milliseconds = _interval.integerValue;
    if (milliseconds < 10) { [self alert:@"定时间隔不能小于 10 ms"]; return; }
    _sendTimer = [NSTimer scheduledTimerWithTimeInterval:milliseconds / 1000.0 target:self
        selector:@selector(timerFired:) userInfo:nil repeats:YES];
    _interval.enabled = NO; _loopCount.enabled = NO; _loopSend.enabled = NO;
    _timerButton.title = @"停止定时"; _quickTimerButton.title = @"停止定时"; _loopButton.enabled = NO;
    [self appendText:[NSString stringWithFormat:@"\n[已开始定时发送：%ld ms]\n", (long)milliseconds]];
}

- (void)stopTimer {
    if (!_sendTimer) return;
    [_sendTimer invalidate]; _sendTimer = nil;
    _interval.enabled = YES; _loopCount.enabled = YES; _loopSend.enabled = YES;
    _timerButton.title = @"开始定时"; _quickTimerButton.title = @"开始定时"; _loopButton.enabled = YES;
    [self appendText:@"\n[已停止定时发送]\n"];
}

- (void)toggleLoop:(id)sender {
    if ([_loopButton.title isEqualToString:@"循环发送"] && _sendTimer) {
        [self alert:@"请先停止定时发送"]; return;
    }
    BOOL loopEnabled = _loopSend.state == NSControlStateValueOn;
    NSString *input = _send.string;
    if ([_loopButton.title isEqualToString:@"循环发送"] && !_connected) { [self alert:@"请先连接或开始监听"]; return; }
    if ([_loopButton.title isEqualToString:@"循环发送"] && !input.length) { [self alert:@"请输入要发送的数据"]; return; }
    BOOL hex = _hexSend.state == NSControlStateValueOn;
    NSString *eol = _eol.stringValue;
    int count = loopEnabled ? (int)_loopCount.integerValue : 1;
    int ms = (int)_interval.integerValue;
    if ([_loopButton.title isEqualToString:@"循环发送"] && ms < 10) { [self alert:@"发送间隔不能小于 10 ms"]; return; }
    char *raw = GoToggleLoop((char *)input.UTF8String, hex ? 1 : 0, (char *)eol.UTF8String, count, ms);
    NSString *result = [NSString stringWithUTF8String:raw ?: ""]; free(raw);
    if ([result isEqualToString:@"started"]) {
        _loopButton.title = @"停止循环";
        _interval.enabled = NO; _loopCount.enabled = NO; _loopSend.enabled = NO; _timerButton.enabled = NO; _quickTimerButton.enabled = NO;
    } else if ([result isEqualToString:@"stopped"]) {
        _loopButton.title = @"循环发送";
        _interval.enabled = YES; _loopCount.enabled = YES; _loopSend.enabled = YES; _timerButton.enabled = YES; _quickTimerButton.enabled = YES;
    } else if ([result hasPrefix:@"error:"]) {
        [self alert:[result substringFromIndex:6]];
    }
}

- (void)loopDone {
    _loopButton.title = @"循环发送";
    _interval.enabled = YES; _loopCount.enabled = YES; _loopSend.enabled = YES; _timerButton.enabled = YES; _quickTimerButton.enabled = YES;
}

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
    _endpointLabel.hidden = !usesSerialName;
    _endpointLabel.stringValue = @"串口设备";
    _ports.hidden = !usesSerialName; _refresh.hidden = !usesSerialName;
    for (NSView *c in _serialControls) c.hidden = !usesSerialName;
    _protocolLabel.hidden = !bridge; _bridgeProtocol.hidden = !bridge;
    _roleLabel.hidden = !usesNet; _role.hidden = !usesNet;
    _ipLabel.hidden = !(usesNet || http); _ip.hidden = !(usesNet || http);
    _ipLabel.stringValue = http ? @"请求 URL" : (net ? (server ? @"监听地址" : @"服务器 IP") : @"IP 地址");
    _portLabel.hidden = !usesNet; _port.hidden = !usesNet;

    _endpointForm.hidden = !usesSerialName;
    _serialForm.hidden = !usesSerialName;
    _networkForm.hidden = !usesNet;
    _protocolField.hidden = !bridge;
    _addressForm.hidden = !(usesNet || http);
    _portField.hidden = !usesNet;

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
    [self layoutConnectionPane];
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
    if ([notification object] != _dataTable) return;
    NSInteger selected = _dataTable.selectedRowIndexes.count;
    __block NSInteger rx = 0, tx = 0, bytes = 0;
    [_dataTable.selectedRowIndexes enumerateIndexesUsingBlock:^(NSUInteger idx, BOOL *stop) {
        NSDictionary *p = _visiblePackets[idx];
        if ([p[@"dir"] isEqualToString:@"RX"]) rx++; else if ([p[@"dir"] isEqualToString:@"TX"]) tx++;
        bytes += [p[@"rawLen"] integerValue];
    }];
    _selectionLabel.stringValue = [NSString stringWithFormat:@"已选择 %ld 条  RX %ld  TX %ld  数据量 %ld B", (long)selected, (long)rx, (long)tx, (long)bytes];
    if (!_detailView) return;
    NSInteger row = _dataTable.selectedRow;
    if (row < 0 || row >= (NSInteger)_visiblePackets.count) { _detailView.string = @""; return; }
    NSDictionary *p = _visiblePackets[row];
    NSString *detail = [NSString stringWithFormat:@"[%@] %@  %@  %@\n协议：%@  状态：%@  响应：%@\nHEX:   %@\nASCII: %@",
        p[@"ts"] ?: @"", p[@"dir"] ?: @"", p[@"len"] ?: @"",
        p[@"kind"] ?: @"", p[@"protocol"] ?: @"", p[@"status"] ?: @"正常", p[@"response"] ?: @"-",
        p[@"hex"] ?: @"", p[@"ascii"] ?: @""];
    _detailView.string = detail;
}

- (void)selectAllPackets:(id)sender {
    if (_visiblePackets.count) [_dataTable selectRowIndexes:[NSIndexSet indexSetWithIndexesInRange:NSMakeRange(0, _visiblePackets.count)] byExtendingSelection:NO];
}
- (void)clearPacketSelection:(id)sender { [_dataTable deselectAll:nil]; }
- (void)invertPacketSelection:(id)sender {
    NSMutableIndexSet *indexes = [NSMutableIndexSet indexSet];
    for (NSUInteger i = 0; i < _visiblePackets.count; i++)
        if (![_dataTable.selectedRowIndexes containsIndex:i]) [indexes addIndex:i];
    [_dataTable selectRowIndexes:indexes byExtendingSelection:NO];
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
    BOOL isDirectory = NO;
    if ([path hasPrefix:@"错误:"] || ![[NSFileManager defaultManager] fileExistsAtPath:path isDirectory:&isDirectory] || !isDirectory) {
        [self alert:path.length ? [NSString stringWithFormat:@"数据库目录不存在：%@", path] : @"数据库目录不可用"]; return;
    }
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
    [_dataTable deselectAll:nil];
    [self tableViewSelectionDidChange:[NSNotification notificationWithName:@"selection" object:_dataTable]];
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
    NSHashTable *selectedPackets = [NSHashTable hashTableWithOptions:NSPointerFunctionsObjectPointerPersonality];
    [_dataTable.selectedRowIndexes enumerateIndexesUsingBlock:^(NSUInteger idx, BOOL *stop) {
        if (idx < _visiblePackets.count) [selectedPackets addObject:_visiblePackets[idx]];
    }];
    [_dataTable deselectAll:nil];
    NSString *kw = [_searchField.stringValue lowercaseString];
    NSString *dir = _dirFilter ? _dirFilter.titleOfSelectedItem : @"全部";
    NSString *type = _typeFilter ? _typeFilter.titleOfSelectedItem : @"全部";
    NSInteger length = _lengthFilter.indexOfSelectedItem;
    NSInteger timeRange = _timeFilter.indexOfSelectedItem;
    NSTimeInterval since = 0;
    if (timeRange == 1) since = [NSDate date].timeIntervalSince1970 - 60;
    else if (timeRange == 2) since = [NSDate date].timeIntervalSince1970 - 300;
    else if (timeRange == 3) since = [NSDate date].timeIntervalSince1970 - 1800;
    [_visiblePackets removeAllObjects];
    for (NSDictionary *p in _packets) {
        if (since > 0 && [p[@"epoch"] doubleValue] < since) continue;
        if (_dirFilter.indexOfSelectedItem > 0 && ![p[@"dir"] isEqualToString:dir]) continue;
        if (_typeFilter.indexOfSelectedItem > 0 && ![p[@"kind"] isEqualToString:type]) continue;
        NSInteger bytes = [p[@"rawLen"] integerValue];
        if (length == 1 && (bytes < 1 || bytes > 8)) continue;
        if (length == 2 && (bytes < 9 || bytes > 64)) continue;
        if (length == 3 && bytes < 65) continue;
        if (kw.length) {
            if (![[p[@"hex"] lowercaseString] containsString:kw] &&
                ![[p[@"ascii"] lowercaseString] containsString:kw]) continue;
        }
        [_visiblePackets addObject:p];
    }
    [_dataTable reloadData];
    NSMutableIndexSet *selection = [NSMutableIndexSet indexSet];
    [_visiblePackets enumerateObjectsUsingBlock:^(id packet, NSUInteger idx, BOOL *stop) {
        if ([selectedPackets containsObject:packet]) [selection addIndex:idx];
    }];
    [_dataTable selectRowIndexes:selection byExtendingSelection:NO];
    [self tableViewSelectionDidChange:[NSNotification notificationWithName:@"selection" object:_dataTable]];
    if (_visiblePackets.count > 0)
        [_dataTable scrollRowToVisible:(NSInteger)_visiblePackets.count - 1];
}

- (void)clearFilter {
    _searchField.stringValue = @"";
    if (_dirFilter) [_dirFilter selectItemAtIndex:0];
    if (_typeFilter) [_typeFilter selectItemAtIndex:0];
    if (_lengthFilter) [_lengthFilter selectItemAtIndex:0];
    if (_timeFilter) [_timeFilter selectItemAtIndex:0];
    [self applyFilter];
}
- (void)controlTextDidChange:(NSNotification *)notification {
    if (notification.object == _searchField) [self applyFilter];
}
- (void)updatePacketStats {
    if (!_statsLabel) return;
    _statsLabel.stringValue = [NSString stringWithFormat:@"RX %ld包  TX %ld包", (long)_rxCount, (long)_txCount];
}

- (void)addPacketWithTS:(NSString *)ts dir:(NSString *)dir hex:(NSString *)hex ascii:(NSString *)ascii kind:(NSString *)kind len:(NSInteger)len {
    NSString *protocol = _mode.titleOfSelectedItem ?: @"未知";
    NSString *status = @"正常";
    if ([protocol isEqualToString:@"串口"] || [protocol isEqualToString:@"串口服务器"]) {
        NSArray *tokens = [hex componentsSeparatedByString:@" "];
        if (tokens.count > 1 && (strtoul([tokens[1] UTF8String], NULL, 16) & 0x80)) status = @"异常";
    }
    NSDictionary *p = @{
        @"ts": ts, @"dir": dir, @"hex": hex, @"ascii": ascii,
        @"kind": kind, @"protocol": protocol, @"status": status, @"response": @"-", @"rawLen": @(len),
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
    [self applyFilter];
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

- (void)analyzeSelected:(id)sender {
    if (!_dataTable.selectedRowIndexes.count) { [self alert:@"请先选择报文"]; return; }
    [_analysisScope selectItemWithTitle:@"选中数据"];
    [self openAnalysisCenter:nil];
    [self runLocalAnalysis:nil];
}

- (void)aiAnalyzeSelected:(id)sender {
    NSIndexSet *rows = _dataTable.selectedRowIndexes;
    if (!rows.count) { [self alert:@"请先选择报文"]; return; }
    NSMutableString *input = [NSMutableString string];
    [rows enumerateIndexesUsingBlock:^(NSUInteger idx, BOOL *stop) {
        [input appendFormat:@"%@ %@\n", _visiblePackets[idx][@"dir"] ?: @"", _visiblePackets[idx][@"hex"] ?: @""];
    }];
    NSString *transport = [_mode.titleOfSelectedItem copy];
    NSButton *button = (NSButton *)sender;
    button.enabled = NO;
    _detailView.string = @"AI 深度分析请求中……\n\n本地通信不会被阻塞。";
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{
        char *raw = GoAIAnalyze((char *)transport.UTF8String, (char *)input.UTF8String);
        NSString *result = [[NSString alloc] initWithUTF8String:raw ?: "AI 分析失败"];
        free(raw);
        dispatch_async(dispatch_get_main_queue(), ^{
            _detailView.string = [NSString stringWithFormat:@"AI 深度分析（仅本次主动调用）\n\n%@", result];
            button.enabled = YES;
            [result release];
            [transport release];
        });
    });
}

- (void)analyzePacket:(id)sender {
    NSInteger row = _dataTable.clickedRow;
    if (row < 0 || row >= (NSInteger)_visiblePackets.count) return;
    NSDictionary *p = _visiblePackets[row];
    char *raw = GoAnalyzePacket((char *)[_mode.titleOfSelectedItem UTF8String], (char *)[p[@"hex"] UTF8String]);
    NSString *result = [NSString stringWithFormat:@"\n%@", [NSString stringWithUTF8String:raw ?: "分析失败"]];
    free(raw);
    if (_detailView) {
        _detailView.string = [_detailView.string stringByAppendingString:result];
        [_detailView scrollRangeToVisible:NSMakeRange(_detailView.string.length, 0)];
    }
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

void UIAddPacket(const char *ts, const char *dir, const char *hex, const char *ascii, const char *kind, int len) {
    NSString *nts    = [[NSString alloc] initWithUTF8String:ts    ?: ""];
    NSString *ndir   = [[NSString alloc] initWithUTF8String:dir   ?: ""];
    NSString *nhex   = [[NSString alloc] initWithUTF8String:hex   ?: ""];
    NSString *nascii = [[NSString alloc] initWithUTF8String:ascii ?: ""];
    NSString *nkind  = [[NSString alloc] initWithUTF8String:kind  ?: ""];
    NSInteger nlen = len;
    dispatch_async(dispatch_get_main_queue(), ^{
        [(AppDelegate *)NSApp.delegate addPacketWithTS:nts dir:ndir hex:nhex ascii:nascii kind:nkind len:nlen];
        [nts release]; [ndir release]; [nhex release]; [nascii release]; [nkind release];
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
