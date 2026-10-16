//! Built-in palettes. Terminal uses the user's existing ANSI colors.
//! Palette sources: https://github.com/catppuccin/catppuccin and
//! https://github.com/morhetz/gruvbox (both MIT licensed).
//! Attribution and license terms are in THIRD_PARTY_NOTICES.md.
use crate::syntax::Style;
use crossterm::style::Color;

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Theme {
    #[default]
    CatppuccinMocha,
    GruvboxDark,
    CatppuccinLatte,
    Terminal,
}

impl Theme {
    pub const CHOICES: &str = "mocha|gruvbox|latte|terminal";

    pub fn name(self) -> &'static str {
        match self {
            Self::CatppuccinMocha => "catppuccin-mocha",
            Self::GruvboxDark => "gruvbox-dark",
            Self::CatppuccinLatte => "catppuccin-latte",
            Self::Terminal => "terminal",
        }
    }

    pub fn parse(name: &str) -> Option<Self> {
        match name {
            "catppuccin-mocha" | "mocha" => Some(Self::CatppuccinMocha),
            "gruvbox-dark" | "gruvbox" => Some(Self::GruvboxDark),
            "catppuccin-latte" | "latte" => Some(Self::CatppuccinLatte),
            "terminal" => Some(Self::Terminal),
            _ => None,
        }
    }

    pub fn palette(self) -> Palette {
        match self {
            Self::CatppuccinMocha => Palette {
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
            },
            Self::GruvboxDark => Palette {
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
            },
            Self::CatppuccinLatte => Palette {
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
            },
            Self::Terminal => Palette {
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
}

impl Palette {
    pub fn syntax(self, style: Style) -> Color {
        match style {
            Style::Plain | Style::Punctuation => self.foreground,
            Style::Keyword => self.keyword,
            Style::Comment => self.comment,
            Style::String => self.string,
            Style::Number => self.number,
            Style::Macro => self.macro_color,
            Style::Lifetime => self.lifetime,
        }
    }
}
