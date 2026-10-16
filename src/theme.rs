//! Built-in palettes. Terminal uses the user's existing ANSI colors.
//! Catppuccin and Gruvbox palette sources: https://github.com/catppuccin/catppuccin and
//! https://github.com/morhetz/gruvbox (both MIT licensed).
//! Monochrome uses an original grayscale palette.
//! Attribution and license terms are in THIRD_PARTY_NOTICES.md.
use crate::syntax::Style;
use crossterm::style::Color;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Theme {
    #[default]
    CatppuccinMocha,
    GruvboxDark,
    CatppuccinLatte,
    Monochrome,
    Terminal,
}

impl Theme {
    pub const CHOICES: &str = "mocha|gruvbox|latte|monochrome|terminal";
    pub const COMPLETIONS: &[&str] = &[
        "mocha",
        "gruvbox",
        "latte",
        "monochrome",
        "terminal",
        "catppuccin-mocha",
        "catppuccin-latte",
        "gruvbox-dark",
    ];

    pub fn name(self) -> &'static str {
        match self {
            Self::CatppuccinMocha => "catppuccin-mocha",
            Self::GruvboxDark => "gruvbox-dark",
            Self::CatppuccinLatte => "catppuccin-latte",
            Self::Monochrome => "monochrome",
            Self::Terminal => "terminal",
        }
    }

    pub fn parse(name: &str) -> Option<Self> {
        match name {
            "catppuccin-mocha" | "mocha" => Some(Self::CatppuccinMocha),
            "gruvbox-dark" | "gruvbox" => Some(Self::GruvboxDark),
            "catppuccin-latte" | "latte" => Some(Self::CatppuccinLatte),
            "monochrome" => Some(Self::Monochrome),
            "terminal" => Some(Self::Terminal),
            _ => None,
        }
    }

    pub fn palette(self) -> Palette {
        match self {
            Self::CatppuccinMocha => Palette {
                bold_keywords: false,
                background: rgb(0x1e1e2e),
                foreground: rgb(0xcdd6f4),
                muted: rgb(0x7f849c),
                surface: rgb(0x313244),
                keyword: rgb(0xcba6f7),
                comment: rgb(0x7f849c),
                string: rgb(0xa6e3a1),
                number: rgb(0xfab387),
                macro_color: rgb(0x89b4fa),
                lifetime: rgb(0xf9e2af),
                type_color: rgb(0xf9e2af),
                function: rgb(0x89b4fa),
                property: rgb(0xb4befe),
                constant: rgb(0xfab387),
                namespace: rgb(0x94e2d5),
                error: rgb(0xf38ba8),
                warning: rgb(0xf9e2af),
                info: rgb(0x89dceb),
                hint: rgb(0x94e2d5),
            },
            Self::GruvboxDark => Palette {
                bold_keywords: false,
                background: rgb(0x282828),
                foreground: rgb(0xebdbb2),
                muted: rgb(0x928374),
                surface: rgb(0x3c3836),
                keyword: rgb(0xfb4934),
                comment: rgb(0x928374),
                string: rgb(0xb8bb26),
                number: rgb(0xd3869b),
                macro_color: rgb(0xfabd2f),
                lifetime: rgb(0x8ec07c),
                type_color: rgb(0xfabd2f),
                function: rgb(0xb8bb26),
                property: rgb(0x83a598),
                constant: rgb(0xd3869b),
                namespace: rgb(0x8ec07c),
                error: rgb(0xfb4934),
                warning: rgb(0xfabd2f),
                info: rgb(0x83a598),
                hint: rgb(0x8ec07c),
            },
            Self::CatppuccinLatte => Palette {
                bold_keywords: false,
                background: rgb(0xeff1f5),
                foreground: rgb(0x4c4f69),
                muted: rgb(0x6c6f85),
                surface: rgb(0xccd0da),
                keyword: rgb(0x8839ef),
                comment: rgb(0x6c6f85),
                string: rgb(0x40a02b),
                number: rgb(0xfe640b),
                macro_color: rgb(0x1e66f5),
                lifetime: rgb(0xdf8e1d),
                type_color: rgb(0xdf8e1d),
                function: rgb(0x1e66f5),
                property: rgb(0x7287fd),
                constant: rgb(0xfe640b),
                namespace: rgb(0x179299),
                error: rgb(0xd20f39),
                warning: rgb(0xdf8e1d),
                info: rgb(0x209fb5),
                hint: rgb(0x179299),
            },
            Self::Monochrome => Palette {
                bold_keywords: true,
                background: rgb(0x080808),
                foreground: rgb(0xb0b0b0),
                muted: rgb(0x5e5e5e),
                surface: rgb(0x303030),
                keyword: rgb(0xffffff),
                comment: rgb(0x7c7c7c),
                string: rgb(0xd8d8d8),
                number: rgb(0x989898),
                macro_color: rgb(0xffffff),
                lifetime: rgb(0xd8d8d8),
                type_color: rgb(0xd8d8d8),
                function: rgb(0xffffff),
                property: rgb(0xb0b0b0),
                constant: rgb(0x989898),
                namespace: rgb(0x989898),
                error: rgb(0xffffff),
                warning: rgb(0xd8d8d8),
                info: rgb(0xb0b0b0),
                hint: rgb(0x7c7c7c),
            },
            Self::Terminal => Palette {
                bold_keywords: false,
                background: Color::Reset,
                foreground: Color::Reset,
                muted: Color::DarkGrey,
                surface: Color::Reset,
                keyword: Color::Magenta,
                comment: Color::DarkGrey,
                string: Color::Green,
                number: Color::Cyan,
                macro_color: Color::Yellow,
                lifetime: Color::Blue,
                type_color: Color::Yellow,
                function: Color::Blue,
                property: Color::Cyan,
                constant: Color::Cyan,
                namespace: Color::Blue,
                error: Color::Red,
                warning: Color::Yellow,
                info: Color::Cyan,
                hint: Color::Cyan,
            },
        }
    }
}

fn rgb(hex: u32) -> Color {
    Color::Rgb {
        r: (hex >> 16) as u8,
        g: (hex >> 8) as u8,
        b: hex as u8,
    }
}

#[derive(Clone, Copy)]
pub struct Palette {
    bold_keywords: bool,
    pub background: Color,
    pub foreground: Color,
    pub muted: Color,
    pub surface: Color,
    keyword: Color,
    comment: Color,
    string: Color,
    number: Color,
    macro_color: Color,
    lifetime: Color,
    type_color: Color,
    function: Color,
    property: Color,
    constant: Color,
    namespace: Color,
    error: Color,
    warning: Color,
    info: Color,
    hint: Color,
}

impl Palette {
    pub fn bold(self, style: Style) -> bool {
        self.bold_keywords && matches!(style, Style::Keyword | Style::Macro)
    }

    pub fn syntax(self, style: Style) -> Color {
        match style {
            Style::Punctuation => self.foreground,
            Style::Keyword => self.keyword,
            Style::Comment => self.comment,
            Style::String => self.string,
            Style::Number => self.number,
            Style::Macro => self.macro_color,
            Style::Lifetime => self.lifetime,
            Style::Type => self.type_color,
            Style::Function => self.function,
            Style::Plain | Style::Variable => self.foreground,
            Style::Property => self.property,
            Style::Constant => self.constant,
            Style::Namespace => self.namespace,
        }
    }

    pub fn diagnostic(self, severity: u8) -> Color {
        match severity {
            1 => self.error,
            2 => self.warning,
            3 => self.info,
            _ => self.hint,
        }
    }
}
