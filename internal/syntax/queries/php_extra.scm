; Tiegel additions to the upstream PHP highlights (php.scm).
; Appended after php.scm; for identical nodes the later pattern wins.

; Declaration names
(class_declaration name: (name) @type)
(interface_declaration name: (name) @type)
(trait_declaration name: (name) @type)
(enum_declaration name: (name) @type)
(enum_case name: (name) @constant)
(base_clause [(name) (qualified_name)] @type)
(class_interface_clause [(name) (qualified_name)] @type)
(use_declaration [(name) (qualified_name)] @type)
(class_constant_access_expression . [(name) (qualified_name)] @type)

; "$" belongs to the variable (upstream marks every "$" as operator)
(variable_name "$" @variable)
(property_element (variable_name "$" @property))
(member_access_expression name: (variable_name "$" @property))

; Attributes: #[Foo(...)]
(attribute [(name) (qualified_name)] @attribute)
(attribute_group "#[" @attribute "]" @attribute)

; Builtin primitive keywords that are not part of the upstream list
["self" "parent" "static" "readonly" "abstract" "final" "list" "array" "unset" "print" "global"] @keyword

; Operators
[
  "=" "+=" "-=" "*=" "/=" "%=" "**=" ".=" "&=" "|=" "^=" "<<=" ">>=" "??="
  "+" "-" "*" "/" "%" "**" "." "&" "|" "^" "~" "<<" ">>"
  "==" "===" "!=" "!==" "<>" "<" ">" "<=" ">=" "<=>"
  "&&" "||" "!" "??" "?" ":" "++" "--" "@" "|>"
  "->" "?->" "::" "=>" "..."
] @operator

; Punctuation
["(" ")" "[" "]" "{" "}"] @punctuation.bracket
["," ";" "\\"] @punctuation.delimiter
